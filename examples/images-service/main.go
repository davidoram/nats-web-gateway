// Command images-service demonstrates a binary NATS service for the gateway
// configuration in docs/configuration.md.
package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"

	"github.com/nats-io/nats.go"
)

func main() {
	url := os.Getenv("NATS_URL")
	if url == "" {
		url = nats.DefaultURL
	}
	nc, err := nats.Connect(url)
	if err != nil {
		log.Fatal(err)
	}
	defer nc.Drain() //nolint:errcheck // process exit is already in progress

	imageData, err := examplePNG()
	if err != nil {
		log.Fatal(err)
	}
	_, err = nc.Subscribe("images.logo", func(message *nats.Msg) {
		reply := nats.NewMsg(message.Reply)
		reply.Header.Set("Content-Type", "image/png")
		reply.Data = imageData
		if publishErr := nc.PublishMsg(reply); publishErr != nil {
			log.Printf("publish PNG reply: %v", publishErr)
		}
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		log.Fatal(err)
	}
	log.Print("image service listening on images.logo")
	select {}
}

func examplePNG() ([]byte, error) {
	canvas := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			canvas.Set(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 4), B: 180, A: 255})
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
