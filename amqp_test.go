package amqp_test

import (
	"fmt"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	amqp "github.com/seatgeek/go-amqp"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var rabbitURL = os.Getenv("AMQP_URL")
var parsedUrl, parseErr = url.Parse(rabbitURL)

var hostname = parsedUrl.Hostname()
var port = "5672"

var _ = Describe("AMQP", func() {
	if parseErr != nil {
		panic(parseErr)
	}

	if parsedUrl.Port() != "" {
		port = parsedUrl.Port()
	}

	var toxicHost = "127.0.0.1:56721"
	var toxicUrl = strings.Replace(rabbitURL, ":"+port, "", 1)
	toxicUrl = strings.Replace(toxicUrl, hostname, toxicHost, 1)

	exec.Command("toxiproxy-cli", "delete", "rabbit").Run()

	Describe("Toletares disconnections", func() {
		Context("AMQP", func() {
			BeforeEach(func() {
				cmd := exec.Command("toxiproxy-cli", "create", "rabbit", "-l", toxicHost, "-u", hostname+":"+port)
				cmd.Run()
			})

			AfterEach(func() {
				cmd := exec.Command("toxiproxy-cli", "delete", "rabbit")
				cmd.Run()
			})

			It("Works under normal conditions", func() {
				conn, err := amqp.NewPublisher(rabbitURL)
				Expect(err).To(BeNil())
				defer conn.Close()

				err = conn.Publish("test", "test", "message")
				Expect(err).To(BeNil())

				err = conn.PublishWithOptions("test", "test", "message", amqp.Options{Gzipped: true})
				Expect(err).To(BeNil())
			})

			It("Works through toxiproxy", func() {
				conn, err := amqp.NewPublisher(toxicUrl)
				Expect(err).To(BeNil())
				defer conn.Close()

				err = conn.Publish("test", "test", "message")
				Expect(err).To(BeNil())
				conn.Close()
			})

			It("Reconnects after rabbit is down", func() {
				conn, err := amqp.NewPublisher(toxicUrl)
				Expect(err).To(BeNil())
				fmt.Println("Dropping all rabbit connections")
				exec.Command("toxiproxy-cli", "toggle", "rabbit").Run()

				defer conn.Close()

				time.Sleep(1 * time.Second)

				wg := &sync.WaitGroup{}

				go func() {
					wg.Add(1)
					time.Sleep(2 * time.Second)
					exec.Command("toxiproxy-cli", "toggle", "rabbit").Run()
					fmt.Println("Accepting Connections again")
					wg.Done()
				}()

				publish := func() {
					defer wg.Done()
					defer GinkgoRecover()
					err = conn.Publish("test", "test", "message")
					Expect(err).To(BeNil())
				}

				wg.Add(4)
				go publish()
				go publish()
				go publish()
				go publish()

				wg.Wait()
			})
		})
	})
})
