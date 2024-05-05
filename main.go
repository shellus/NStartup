package main

import (
	"log"
	"nstartup-server/server"
	"os"
)

var mainLog = log.New(os.Stdout, "[Main] ", log.LstdFlags)

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
	err = mainServer.Start()
	if err != nil {
		panic(err)
	}
	mainLog.Println("Exit!")
}
