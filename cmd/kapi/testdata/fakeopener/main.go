// Command fakeopener stands in for the system browser opener and records the URL it was asked to open.
package main

import (
	"os"
)

func main() {
	f, err := os.OpenFile(os.Getenv("KAPI_FAKE_OPENED"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(1)
	}
	defer f.Close()
	if _, err := f.WriteString(os.Args[len(os.Args)-1] + "\n"); err != nil {
		os.Exit(1)
	}
}
