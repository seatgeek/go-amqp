package amqp

import (
	"bytes"
	"compress/gzip"
	"fmt"
	log "github.com/sirupsen/logrus"
	"math"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/streadway/amqp"
)

// Publisher is an AMQP connection with a channel setup to publish messages
type Publisher interface {

	// Publishes a messages to a given exchange and routing key. This message assumes the body
	// is encoded as a JSON string and that you want messages to be persistent.
	Publish(exchange, routingKey, body string) error

	// Publishes a messages to a given exchange and routing key, allowing you to pass extra options
	PublishWithOptions(exchange, routingKey, body string, options Options) error

	// Closes the channel to the AMQP server.
	Close()
}

// Options contains the possible additional attributes for an AMQP message
type Options struct {

	// Whether or not to set the ContentType heared to application/json
	AsJSON bool

	// Whether or not to automatically gzip the message contents
	Gzipped bool

	// Whether you want messages to be persistent in the AMQP server or make them transient
	Persistent bool
}

// AMQPConnection is n opaque struct representing a channel to the AMQP server. It is automatically setup
// so it reconnects in case of a connection loss.
type AMQPConnection struct {
	channel   *amqp.Channel
	url       string
	semaphore *sync.WaitGroup
	closing   bool
}

// NewPublisher returns a new Publisher that is capable of automatically reconnect to the server when needed
func NewPublisher(amqpURL string) (Publisher, error) {
	return newAMQP(amqpURL)
}

func (a *AMQPConnection) Publish(exchange, routingKey, body string) error {
	return a.PublishWithOptions(exchange, routingKey, body, Options{AsJSON: true, Gzipped: false, Persistent: true})
}

func (a *AMQPConnection) PublishWithOptions(exchange, routingKey, body string, options Options) (err error) {
	if a.closing {
		err = fmt.Errorf("Couldn't publish AMQP message: %s", err)
		log.WithFields(log.Fields{
			"logger":      "amqp",
			"exchange":    exchange,
			"routing_key": routingKey,
			"body":        body,
		}).Errorf("%v", err)
		return err
	}

	// Make sure we are not in the middle of a reconnection attempt
	a.semaphore.Wait()

	channel := a.channel

	contentType := "text/plain"
	contentEnconding := "UTF-8"
	messageBody := []byte(body)
	persistent := amqp.Persistent

	if options.AsJSON {
		contentType = "application/json"
	}

	if options.Gzipped {
		contentEnconding = "gzip"
		var buff bytes.Buffer
		gz := gzip.NewWriter(&buff)
		if _, err := gz.Write(messageBody); err != nil {
			return err
		}
		if err := gz.Flush(); err != nil {
			return err
		}
		gz.Close()
		messageBody = buff.Bytes()
	}

	if !options.Persistent {
		persistent = amqp.Transient
	}

	// Publish message
	if err = channel.Publish(
		exchange,   // publish to an exchange
		routingKey, // routing to 0 or more queues
		true,       // mandatory
		false,      // immediate
		amqp.Publishing{
			Headers:         amqp.Table{},
			ContentType:     contentType,
			ContentEncoding: contentEnconding,
			Body:            messageBody,
			DeliveryMode:    persistent,
			Priority:        0, // 0-9
		},
	); err != nil {
		err = fmt.Errorf("Couldn't publish AMQP message: %s", err)
		log.WithFields(log.Fields{"logger": "amqp"}).Errorf("%v", err)
		return err
	}

	return nil
}

func (a *AMQPConnection) Close() {
	a.closing = true
	go a.channel.Close()
}

func dial(amqpURL string) (*amqp.Connection, error) {
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func newAMQP(amqpURL string) (*AMQPConnection, error) {
	// Parse the URL
	parsedURL, err := url.Parse(amqpURL)

	if err != nil {
		return nil, fmt.Errorf("Could not parse connection string: %v", err)
	}

	// split out multiple hosts and try each, round robbin
	hosts := strings.Split(parsedURL.Host, ",")

	// Try the various hosts until we are successful
	for _, host := range hosts {
		parsedURL.Host = host
		hostURL := parsedURL.String()
		conn, err := dial(hostURL)

		if err != nil {
			log.WithFields(log.Fields{"logger": "amqp", "url": amqpURL}).Warningf("Error creating connection to AMQP: %v", err)
			continue
		}

		channel, err := conn.Channel()

		if err != nil {
			log.WithFields(log.Fields{"logger": "amqp", "url": amqpURL}).Warningf("Error creating AMQP channel: %v", err)
			continue
		}

		// Setup acknowledgement
		connection := &AMQPConnection{channel: channel, url: amqpURL, semaphore: &sync.WaitGroup{}, closing: false}
		connection.monitor()

		// Connected, return
		log.WithFields(log.Fields{"logger": "amqp", "url": amqpURL}).Info("Successfully connected to AMQP")
		return connection, nil
	}

	// Never returned, couldn't connect to any host
	return nil, fmt.Errorf("Couldn't connect to AMQP via %s: %e", amqpURL, err)
}

func (conn *AMQPConnection) monitor() {
	closeListener := make(chan *amqp.Error)
	conn.channel.NotifyClose(closeListener)

	go func() {
		err := <-closeListener

		if conn.closing {
			return
		}

		log.WithFields(log.Fields{"logger": "amqp"}).Warningf("Connection to AMQP closed: %v", err)

		conn.semaphore.Add(1)

		isError := err != nil
		retry := 0.0
		for isError {
			wait := time.Duration(math.Min(math.Pow(2, retry)*float64(time.Microsecond), float64(2*time.Second)))
			time.Sleep(wait)
			newConn, err := newAMQP(conn.url)
			isError = err != nil
			retry = retry + 1

			if !isError {
				conn.channel = newConn.channel
				conn.semaphore.Done()
			}
		}
	}()
}
