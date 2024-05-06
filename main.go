package main

import (
	"context"
	"fmt"
	"log"
	"nstartup-server/server"
	"os"
	"strings"
)

var mainLog = log.New(os.Stdout, "[Main] ", log.LstdFlags)
var mainCtx, mainCancel = context.WithCancel(context.Background())
var exitDone = make(chan struct{})

func main() {
	// 打印bus.EventNames
	for k, v := range server.EventNames {
		mainLog.Printf("%d: %s\n", k, v)
	}
	mainServer, err := server.NewServer(nil)
	if err != nil {
		panic(err)
	}

	mainLog.Println("MainServer Start in", mainServer.GetListenAddr())
	go func() {
		err = mainServer.Start(mainCtx)
		if err != nil {
			panic(err)
		}
		exitDone <- struct{}{}
	}()

	// 等待输入内容
	for {
		var input string
		_, _ = fmt.Scanln(&input)
		// 将输入内容前面加上#原样输出作为反馈
		//mainLog.Println("#", input)

		// 判断内容前缀，使用“:”分割取第一部分
		inputArgs := strings.Split(input, ":")
		switch inputArgs[0] {
		case "exit":
			mainCancel()
			<-exitDone
			mainLog.Println("Exit!")
			return
		case "dump":
			dump := mainServer.DumpAgentTable()
			mainLog.Println(dump)
		case "":
			// 空行不报错
		default:
			mainLog.Println("Unknown command")
		}
	}
}
