package main

import (
	"io"
	"bytes"
	"flag"
	"fmt"
	"log"
	"net/http"
)

var (
	addr = flag.String("addr", "127.0.0.1:8080", "TCP host+port for the server")
	world = flag.String("world", "world", "World name")
	data   = flag.String("data", "{}", "Data for the entity")
)

func main() {
	flag.Parse()
	create(*addr, *world, *data)
}

func create(addr, world, data string) error {
	log.Printf("Creating entity in world %s with data %s", world, data)
	resp, err := http.Post(fmt.Sprintf("http://%s/%s", addr, world), "application-type/json", bytes.NewReader([]byte(data)))
	if err != nil {
		log.Fatal(err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Fatal(err)
		}
		bodyString := string(bodyBytes)
		log.Print(bodyString)
	}
	return nil
}
