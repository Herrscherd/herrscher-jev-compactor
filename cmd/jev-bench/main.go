// Command jev-bench replays compaction snapshots under settings files and prints what each setting would have pruned.
package main

import "os"

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr).code())
}
