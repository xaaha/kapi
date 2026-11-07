package main

import (
	"fmt"
	"log"
	"os"
	"time"
)

func main() {
	log.Printf("probe log line")
	fmt.Fprintln(os.Stdout, "probe stdout line")
	_, err := fmt.Fprintln(os.Stderr, "probe stderr line")
	os.WriteFile(os.Args[1], []byte(fmt.Sprintf("stderr write err=%v", err)), 0o600)
	time.Sleep(2 * time.Second)
}
