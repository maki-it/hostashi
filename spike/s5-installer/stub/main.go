// Throwaway stand-in for the future hostashi binary: only proves that the installed units start and run as the right user.
package main

import (
	"fmt"
	"os"
	"time"
)

func main() {
	if len(os.Args) < 2 || (os.Args[1] != "agent" && os.Args[1] != "serve") {
		fmt.Fprintln(os.Stderr, "usage: hostashi agent|serve")
		os.Exit(64)
	}
	fmt.Printf("hostashi stub %s uid=%d\n", os.Args[1], os.Getuid())
	for {
		time.Sleep(time.Hour)
	}
}
