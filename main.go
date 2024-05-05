package main

import (
	"log"
	"nstartup-server/server"
	"os"
)

var mainLog = log.New(os.Stdout, "[Main] ", log.LstdFlags)

func main() {
	mainServer, err := server.NewServer(nil)
	if err != nil {
		panic(err)
	}

	mainLog.Println("mainServer Start in", mainServer.GetListenAddr())
	err = mainServer.Start()
	if err != nil {
		panic(err)
	}
	mainLog.Println("Exit!")
}
