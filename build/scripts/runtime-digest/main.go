package main

import (
	"fmt"
	"os"

	"github.com/hitk/hitk/internal/skillruntime"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: runtime-digest <runtime-root>")
		os.Exit(2)
	}
	digest, err := skillruntime.ComputePayloadDigest(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(digest)
}
