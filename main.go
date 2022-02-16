package main

import (
	"context"
	"encoding/gob"
	"flag"
	"fmt"
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

func fdToPipe(fd uintptr) (f *os.File, err error) {
	f = os.NewFile(uintptr(fd), "pipe")
	if f == nil {
		err = fmt.Errorf("nil pipe for fd %d", fd)
	}
	return
}

// We'll make this crash
func worker() {
	defer fmt.Println("child exiting")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	defer cancel()

	go func() {
		time.Sleep(1 * time.Second)
		fmt.Println("You know I love to crash")
		os.Exit(1)
	}()

	in, err := fdToPipe(3)
	if err != nil {
		panic(err)
	}
	decoder := gob.NewDecoder(in)

	out, err := fdToPipe(4)
	if err != nil {
		panic(err)
	}
	encoder := gob.NewEncoder(out)

	read := make(chan int)
	write := make(chan int)
	e := make(chan Encoder, 1)
	d := make(chan Decoder, 1)
	e <- encoder
	d <- decoder

	c := NewChannel(ctx, e, d, write, read)
	for {
		select {
		case <-ctx.Done():
			fmt.Println("child exiting")
			return
		case v := <-c.Recv:
			c.Send <- v
		}
	}
}

type Encoder interface {
	Encode(e any) error
}
type Decoder interface {
	Decode(e any) error
}

type Channel[sendT, recvT any] struct {
	Send chan<- sendT
	Recv <-chan recvT
}

func NewChannel[sendT, recvT any](
	ctx context.Context,
	encoderChan <-chan Encoder,
	decoderChan <-chan Decoder,
	sendC chan sendT,
	recvC chan recvT,
) Channel[sendT, recvT] {

	go func() {
		var (
			encoder Encoder
			v       sendT
			hasV    bool
		)
	ENCODER:
		for {
			select {
			case <-ctx.Done():
				return
			case encoder = <-encoderChan:
			}
			for {
				for {
					select {
					case <-ctx.Done():
						return
					case encoder = <-encoderChan:
					default:
					}
					if !hasV {
						select {
						case <-ctx.Done(): // queue is not cleared/durable
							return
						case v = <-sendC:
							hasV = true
						}
					}
					err := encoder.Encode(v)
					if err != nil {
						fmt.Println("encoder send", err)
						continue ENCODER
					}
					hasV = false
				}

			}
		}
	}()

	go func() {
		var decoder Decoder

	DECODER:
		for {
			select {
			case <-ctx.Done():
				return
			case decoder = <-decoderChan:
			}
			for {
				select {
				case <-ctx.Done():
					return
				case decoder = <-decoderChan:
				default:
				}
				var v recvT
				err := decoder.Decode(&v)
				if err != nil {
					fmt.Println("decoder receive", err)
					continue DECODER
				}
				select {
				case <-ctx.Done(): // queue is not cleared/durable
					return
				case recvC <- v:
				}
			}
		}
	}()

	return Channel[sendT, recvT]{
		Send: sendC,
		Recv: recvC,
	}
}

func parent() {
	defer fmt.Println("parent exiting")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	defer cancel()

	args := append([]string{}, os.Args[1:]...)
	args = append(args, "-child")

	e := make(chan Encoder, 1)
	d := make(chan Decoder, 1)

	go func() {
		for ctx.Err() == nil {
			func() {
				fmt.Println("spawning new child")
				childOutRead, childOutWrite, err := os.Pipe()
				if err != nil {
					panic(fmt.Errorf("os.Pipe: %w", err))
				}
				defer childOutRead.Close()
				defer childOutWrite.Close()

				childInRead, childInWrite, err := os.Pipe()
				if err != nil {
					panic(fmt.Errorf("os.Pipe: %w", err))
				}
				defer childInRead.Close()
				defer childInWrite.Close()

				cmd := exec.CommandContext(ctx, os.Args[0], args...)
				cmd.Stdin = os.Stdin
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr

				cmd.ExtraFiles = []*os.File{childInRead, childOutWrite}

				if err := cmd.Start(); err != nil {
					fmt.Println("Couldn't spawn child", err)
					return
				}
				defer cmd.Wait()

				d <- gob.NewDecoder(childOutRead)
				e <- gob.NewEncoder(childInWrite)
			}()
		}
	}()

	read := make(chan int)
	write := make(chan int)

	c := NewChannel(ctx, e, d, write, read)
	c.Send <- 1

	const NUM_TRIES = 1 << 20

	var replies = [NUM_TRIES]bool{}

	var (
		msgCount  int
		startTime time.Time
	)

	defer func() {
		dur := time.Now().Sub(startTime)
		rate := float64(msgCount) / dur.Seconds()
		fmt.Printf("%d msgs in %v (%f msgs/s)", msgCount, dur, rate)
	}()

	i := 0
	startTime = time.Now()
	const recv_timeout = time.Second
	var lastRecv = time.NewTimer(recv_timeout)

LOOP:
	for {
		select {
		case <-ctx.Done():
			fmt.Println("parent break")
			break LOOP
		case v := <-c.Recv:
			// fmt.Println("parent received ", v)
			if v < NUM_TRIES {
				lastRecv.Reset(recv_timeout)
				replies[v] = true
			}
			msgCount++
		case <-lastRecv.C:
			fmt.Println("idle timeout")
			break LOOP
		case c.Send <- i:
			if i < NUM_TRIES {
				// fmt.Println("send", i)
				i++
				msgCount++
			}

		}
	}

	missed := 0
	for i := 0; i < NUM_TRIES; i++ {
		if !replies[i] {
			missed++
		}
	}
	fmt.Println("missed", missed)
}
