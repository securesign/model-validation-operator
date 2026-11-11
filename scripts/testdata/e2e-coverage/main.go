// This tiny program supplies real Go binary coverage data to collector tests.
package main

import (
	"fmt"
	"os"
)

func beforeRestart() { fmt.Println("only executed before restart") }
func afterRestart()  { fmt.Println("only executed after restart") }

func main() {
	if len(os.Args) > 1 && os.Args[1] == "before" {
		beforeRestart()
	} else {
		afterRestart()
	}
}
