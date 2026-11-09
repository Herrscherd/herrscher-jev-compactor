// Command jev-bench replays compaction snapshots under settings files and
// prints what each setting would have pruned.
//
// The command ends with one of these exit codes:
//
//	0   the comparison was printed, or the help was asked for
//	2   the command line is wrong
//	65  a snapshot is malformed, has another version or fails a check
//	66  a snapshot or settings file does not exist or is not a regular file
//	74  output or a file could not be read or written
//	78  a settings file is malformed, too large or sets a setting jev refuses
package main

import "os"

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr).code())
}
