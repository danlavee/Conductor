// Command skillcheck validates Conductor's distributable skill and repository contract.
package main

import (
	"fmt"
	"os"

	"github.com/danlavee/Conductor/internal/skillcheck"
)

func main() {
	root, err := skillcheck.FindRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	problems := skillcheck.Validate(root)
	if len(problems) > 0 {
		for _, problem := range problems {
			fmt.Fprintln(os.Stderr, "ERROR:", problem)
		}
		os.Exit(1)
	}
	fmt.Println("Conductor skill package is valid.")
}
