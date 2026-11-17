// Command s1client is a tiny test client: it prints the uid the agent saw.
package main

import (
	"fmt"
	"os"

	"github.com/maki-it/hostashi/spike/s1-agent-rpc/agentrpc"
)

func main() {
	c, err := agentrpc.Dial(os.Args[1])
	if err != nil {
		fmt.Println("dial:", err)
		os.Exit(2)
	}
	defer c.Close()
	r, err := agentrpc.Call[agentrpc.PingReq, agentrpc.PingResp](c, "Ping", agentrpc.PingReq{})
	if err != nil {
		fmt.Println("call:", err)
		os.Exit(3)
	}
	fmt.Println("uid", r.UID)
}
