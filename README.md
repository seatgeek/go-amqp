# go-amqp

A simple to use AMQP publisher interface that is able to automatically reconnect to the server when needed.

## Installation

Install it using the `go get` command:

    go get github.com/seatgeek/go-amqp

## Usage

Simply get a new publisher instance by passing the connection string:

```go
import (
	amqp "github.com/seatgeek/go-amqp"
)

conn, err := amqp.NewPublisher("amqp://guest:guest@127.0.0.1:5672/")

...

// Publish assumes that you are passing a hson enconded string as a message
err = conn.Publish("my_exchange", "my_routing_key", myJsonEncodedString)
```
