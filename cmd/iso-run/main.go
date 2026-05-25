package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("iso-run dev")
		return
	}
	fmt.Fprintln(os.Stderr, "iso-run: pipeline not initialized")
	os.Exit(2)
}
