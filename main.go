package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/rpc"
	"os"
	"os/exec"
	"os/signal"
	"time"
	"unsafe"
)

var (
	child = flag.Bool("child", false, "this is for the child process")
)

func main() {

	flag.Parse()

	if *child {
		worker()
	} else {
		parent()
	}
}

func fromFD(fd uintptr) (f *os.File, err error) {
	f = os.NewFile(uintptr(fd), "unixx")
	if f == nil {
		err = fmt.Errorf("nil for fd %d", fd)
	}
	return
}

// We'll make this crash
func worker() {
	defer fmt.Println("child exiting")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	defer cancel()

	// go func() {
	// 	time.Sleep(1 * time.Second)
	// 	fmt.Println("You know I love to crash")
	// 	os.Exit(1)
	// }()

	in, err := fromFD(3)
	if err != nil {
		panic(err)
	}
	defer in.Close()
	conn, err := net.FileConn(in)
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	client := rpc.NewClient(conn)
	defer client.Close()
	args := &Args{A: 7, B: 8}
	var reply int
	var startTime = time.Now()
	const MAX_COUNT = 1 << 12

	for i := 0; i < MAX_COUNT; i++ {
		if ctx.Err() != nil {
			fmt.Println("ctx done")
			return
		}
		err = client.Call("Arith.Multiply", args, &reply)
		if err != nil {
			log.Fatal("arith error:", err)
		}
	}

	dur := time.Now().Sub(startTime)
	size := unsafe.Sizeof(*args)
	rate := float64(MAX_COUNT) / dur.Seconds()
	fmt.Printf("message size is %d bytes\n", size)
	fmt.Printf("%d msgs in %v (%f req/s)\n", MAX_COUNT, dur, rate)
	fmt.Printf("%f bytes/s (%f mb/s)\n", float64(size)*rate, float64(size)*rate/float64(1<<20))
	fmt.Printf("Arith: %d*%d=%d\n", args.A, args.B, reply)

}

func parent() {
	defer fmt.Println("parent exiting")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	defer cancel()

	args := append([]string{}, os.Args[1:]...)
	args = append(args, "-child")

	ul, err := net.ListenUnix("unix", &net.UnixAddr{})
	if err != nil {
		panic(err)
	}
	defer ul.Close()

	arith := new(Arith)
	rpc.Register(arith)
	go rpc.Accept(ul)

	for ctx.Err() == nil {
		func() {
			fmt.Println("spawning new child")

			conn, err := net.DialUnix("unix", nil, ul.Addr().(*net.UnixAddr))
			if err != nil {
				panic(err)
			}
			defer conn.Close()
			f, err := conn.File()
			if err != nil {
				panic(err)
			}
			defer f.Close()

			cmd := exec.CommandContext(ctx, os.Args[0], args...)
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			cmd.ExtraFiles = []*os.File{f}

			if err := cmd.Start(); err != nil {
				fmt.Println("Couldn't spawn child", err)
				return
			}
			defer cmd.Wait()

		}()
	}

}

// from net/rpc
type Args struct {
	A, B int
	crap [1 << 24]byte // 16 MB for stress
}

type Quotient struct {
	Quo, Rem int
}

type Arith int

func (t *Arith) Multiply(args *Args, reply *int) error {
	*reply = args.A * args.B
	return nil
}

func (t *Arith) Divide(args *Args, quo *Quotient) error {
	if args.B == 0 {
		return errors.New("divide by zero")
	}
	quo.Quo = args.A / args.B
	quo.Rem = args.A % args.B
	return nil
}
