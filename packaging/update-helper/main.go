// Gateway's helper is copied outside the installation before execution. It
// accepts an immutable local plan and independently verifies publisher trust.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"../../internal/updateapply"
)

func main() {
	plan := flag.String("plan", "", "private staged update plan")
	digest := flag.String("plan-sha256", "", "exact approved plan SHA-256")
	recover := flag.Bool("recover", false, "recover an interrupted authenticated application transaction")
	flag.Parse()
	if *plan == "" || *digest == "" || len(flag.Args()) != 0 {
		fmt.Fprintln(os.Stderr, "A verified local update plan and digest are required.")
		os.Exit(2)
	}
	var result updateapply.Result
	var e error
	if *recover {
		result, e = updateapply.Recover(context.Background(), *plan, *digest)
	} else {
		result, e = updateapply.Run(context.Background(), *plan, *digest)
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stdout, result.Message)
}
