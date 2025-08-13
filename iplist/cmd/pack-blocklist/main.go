// Takes P2P blocklist text format in stdin, and outputs the packed format
// from the iplist package.
package main

import (
	"bufio"
	"log"
	"os"

	"github.com/james-lawrence/torrent/iplist"
)

func main() {
	l, err := iplist.NewFromReader(os.Stdin)
	if err != nil {
		log.Fatal(err)
	}
	wb := bufio.NewWriter(os.Stdout)
	defer wb.Flush()
	err = l.WritePacked(wb)
	if err != nil {
		log.Fatal(err)
	}
}
