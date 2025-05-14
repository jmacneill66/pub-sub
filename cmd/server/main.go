package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"log"
	

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/bootdotdev/learn-pub-sub-starter/internal/gamelogic"
	"github.com/bootdotdev/learn-pub-sub-starter/internal/pubsub"
	"github.com/bootdotdev/learn-pub-sub-starter/internal/routing"
)

func main() {
	fmt.Println("Starting Peril server...")

	conn, err := amqp.Dial("amqp://guest:guest@localhost:5672/")
	if err != nil {
		fmt.Printf("Failed to connect to RabbitMQ: %s\n", err)
		return
	}
	defer conn.Close()
	fmt.Println("Connection to RabbitMQ successful!")

	ch, err := conn.Channel()
	if err != nil {
		fmt.Printf("Failed to open a channel: %s\n", err)
		return
	}
	defer ch.Close()

	err = ch.ExchangeDeclare(
		routing.ExchangePerilDirect,
		"direct",
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		fmt.Printf("Failed to declare exchange: %s\n", err)
		return
	}

	// Print commands help
	gamelogic.PrintServerHelp()

	// Signal setup
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// Declare the topic exchange
	err = ch.ExchangeDeclare(
		routing.ExchangePerilTopic,
		"topic",
		true,  // durable
		false, // auto-deleted
		false, // internal
		false, // no-wait
		nil,
	)
	if err != nil {
		fmt.Printf("Failed to declare topic exchange: %s\n", err)
		return
	}

	// Declare durable game_logs queue
	_, err = ch.QueueDeclare(
		routing.GameLogSlug,
		true,  // durable
		false, // autoDelete
		false, // exclusive
		false, // noWait
		nil,   // args
	)
	if err != nil {
		fmt.Printf("Failed to declare game_logs queue: %s\n", err)
		return
	}

	// Bind game_logs queue to topic exchange
	err = ch.QueueBind(
		routing.GameLogSlug,
		routing.GameLogSlug+".*",
		routing.ExchangePerilTopic,
		false,
		nil,
	)
	if err != nil {
		fmt.Printf("Failed to bind game_logs queue: %s\n", err)
		return
	}

	err = pubsub.SubscribeGob(
		conn,
		routing.ExchangePerilTopic,
		routing.GameLogSlug,        // durable queue: "game_logs"
		fmt.Sprintf("%s.*", routing.GameLogSlug), // wildcard routing key
		pubsub.QueueTypeDurable,
		func(gl gamelogic.GameLog) pubsub.AckType {
			defer fmt.Print("> ")
			gamelogic.WriteLog(gl)
			return pubsub.Ack
		},
	)
	if err != nil {
		log.Fatalf("Failed to subscribe to game logs: %s", err)
	}


	// Interactive REPL
	for {
		fmt.Print("> ")
		words := gamelogic.GetInput()
		if len(words) == 0 {
			continue
		}

		command := strings.ToLower(words[0])
		switch command {
		case "pause":
			fmt.Println("Sending pause message...")
			state := routing.PlayingState{IsPaused: true}
			if err := pubsub.PublishJSON(ch, routing.ExchangePerilDirect, routing.PauseKey, state); err != nil {
				fmt.Printf("Failed to publish pause message: %s\n", err)
			}
		case "resume":
			fmt.Println("Sending resume message...")
			state := routing.PlayingState{IsPaused: false}
			if err := pubsub.PublishJSON(ch, routing.ExchangePerilDirect, routing.PauseKey, state); err != nil {
				fmt.Printf("Failed to publish resume message: %s\n", err)
			}
		case "quit":
			fmt.Println("Exiting...")
			return
		case "help":
			gamelogic.PrintServerHelp()
		default:
			fmt.Printf("Unknown command: %s\n", command)
		}
	}
}
