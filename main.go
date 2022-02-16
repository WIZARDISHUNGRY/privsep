package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"time"
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

	_, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	defer cancel()

	go func() {
		time.Sleep(1 * time.Second)
		fmt.Println("You know I love to crash")
		os.Exit(1)
	}()

	in, err := fromFD(3)
	if err != nil {
		panic(err)
	}

	arg := "ok listen up"

	err = json.NewEncoder(in).Encode(arg)
	if err != nil {
		panic(err)
	}

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
	fmt.Println(ul.Addr().String())

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

			{
				conn, err := ul.Accept()
				if err != nil {
					panic(err)
				}

				var data interface{}
				decoder := json.NewDecoder(conn)
				if err := decoder.Decode(&data); err != nil {
					panic(err)
				}
				fmt.Printf("Data received from child pipe: %v\n", data)
			}
		}()
	}

}
