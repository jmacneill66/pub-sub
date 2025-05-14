package main

import (
	
	"fmt"
	"log"
	"os"
	"strings"
	"strconv"
	"time"
	
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/bootdotdev/learn-pub-sub-starter/internal/gamelogic"
	"github.com/bootdotdev/learn-pub-sub-starter/internal/pubsub"
	"github.com/bootdotdev/learn-pub-sub-starter/internal/routing"
	
)

func handlerPause(gs *gamelogic.GameState) func(routing.PlayingState) pubsub.AckType {
	return func(state routing.PlayingState) pubsub.AckType {
		defer fmt.Print("> ")
		gs.HandlePause(state)
		return pubsub.Ack
	}
}

func handlerMove(gs *gamelogic.GameState, ch *amqp.Channel, username string) func(gamelogic.ArmyMove) pubsub.AckType {
	return func(mv gamelogic.ArmyMove) pubsub.AckType {
		defer fmt.Print("> ")
		outcome := gs.HandleMove(mv)

		if outcome == gamelogic.MoveOutcomeMakeWar {
			rw := gamelogic.RecognitionOfWar{
				Attacker: mv.Player,
				Defender: gs.GetPlayerSnap(),
			}
			key := fmt.Sprintf("%s.%s", routing.WarRecognitionsPrefix, username)

			if err := pubsub.PublishJSON(ch, routing.ExchangePerilTopic, key, rw); err != nil {
				fmt.Println("Failed to publish war recognition:", err)
				return pubsub.NackRequeue
			}

			fmt.Println("War recognition published.")
			return pubsub.Ack
		}

		switch outcome {
		case gamelogic.MoveOutComeSafe:
			return pubsub.Ack
		case gamelogic.MoveOutcomeSamePlayer:
			return pubsub.NackDiscard
		default:
			return pubsub.NackDiscard
		}
	}
}

func handlerWar(gs *gamelogic.GameState, ch *amqp.Channel) func(gamelogic.RecognitionOfWar) pubsub.AckType {
	return func(rw gamelogic.RecognitionOfWar) pubsub.AckType {
		defer fmt.Print("> ")
		outcome, winner, loser := gs.HandleWar(rw)

		var message string
		switch outcome {
		case gamelogic.WarOutcomeNotInvolved:
			return pubsub.NackRequeue
		case gamelogic.WarOutcomeNoUnits:
			return pubsub.NackDiscard
		case gamelogic.WarOutcomeYouWon, gamelogic.WarOutcomeOpponentWon:
			message = fmt.Sprintf("%s won a war against %s", winner, loser)
		case gamelogic.WarOutcomeDraw:
			message = fmt.Sprintf("A war between %s and %s resulted in a draw", winner, loser)
		default:
			fmt.Println("Unknown war outcome")
			return pubsub.NackDiscard
		}

		gl := gamelogic.GameLog{
			CurrentTime: time.Now(),
			Message:     message,
			Username:    rw.Attacker.Username,
		}

		key := fmt.Sprintf("%s.%s", routing.GameLogSlug, rw.Attacker.Username)
		if err := pubsub.PublishGob(ch, routing.ExchangePerilTopic, key, gl); err != nil {
			fmt.Println("Failed to publish war log:", err)
			return pubsub.NackRequeue
		}

		return pubsub.Ack
	}
}


func main() {
	fmt.Println("Starting Peril client...")

	username, err := gamelogic.ClientWelcome()
	if err != nil {
		log.Fatalf("Login failed: %s", err)
	}

	conn, err := amqp.Dial("amqp://guest:guest@localhost:5672/")
	if err != nil {
		log.Fatalf("Failed to connect to RabbitMQ: %s", err)
	}
	defer conn.Close()

	queueName := fmt.Sprintf("%s.%s", routing.PauseKey, username)

	ch, _, err := pubsub.DeclareAndBind(
		conn,
		routing.ExchangePerilDirect,
		queueName,
		routing.PauseKey,
		pubsub.QueueTypeTransient,
	)
	if err != nil {
		log.Fatalf("Failed to declare/bind queue: %s", err)
	}
	defer ch.Close()

	// Game state initialization
	gs := gamelogic.NewGameState(username)

	pauseQueue := fmt.Sprintf("%s.%s", routing.PauseKey, username)

	err = pubsub.SubscribeJSON(
		conn,
		routing.ExchangePerilDirect,
		pauseQueue,
		routing.PauseKey,
		pubsub.QueueTypeTransient,
		handlerPause(gs),
	)
	if err != nil {
		log.Fatalf("Failed to subscribe to pause messages: %s", err)
	}

	moveQueue := fmt.Sprintf("army_moves.%s", username)

	err = pubsub.SubscribeJSON(
		conn,
		routing.ExchangePerilTopic,
		moveQueue,
		"army_moves.*",
		pubsub.QueueTypeTransient,
		handlerMove(gs, ch, username),
	)
	if err != nil {
		log.Fatalf("Failed to subscribe to army moves: %s", err)
	}

	err = pubsub.SubscribeJSON(
		conn,
		routing.ExchangePerilTopic, // exchange
		"war",                      // durable shared queue
		"war_recognitions.*",       // routing key pattern
		pubsub.QueueTypeDurable,
		handlerWar(gs, ch),
	)
	if err != nil {
		log.Fatalf("Failed to subscribe to war messages: %s", err)
	}


	// REPL Loop
	for {
		fmt.Print("> ")
		words := gamelogic.GetInput()
		if len(words) == 0 {
			continue
		}

		cmd := strings.ToLower(words[0])
		switch cmd {
		case "spawn":
			if err := gs.CommandSpawn(words); err != nil {
				fmt.Println("Error:", err)
			}
		case "move":
			move, err := gs.CommandMove(words)
			if err != nil {
				fmt.Println("Error:", err)
				break
			}
			fmt.Printf("Moved %d unit(s) to %s.\n", len(move.Units), move.ToLocation)
			// Publish the move
			key := fmt.Sprintf("army_moves.%s", username)
			err = pubsub.PublishJSON(ch, routing.ExchangePerilTopic, key, move)
			if err != nil {
				fmt.Println("Failed to publish move:", err)
			} else {
				fmt.Println("Move published successfully.")
			}
		case "status":
			gs.CommandStatus()
		case "help":
			gamelogic.PrintClientHelp()
		case "spam":
			if len(words) < 2 {
				fmt.Println("Usage: spam <count>")
				break
			}
			count, err := strconv.Atoi(words[1])
			if err != nil || count <= 0 {
				fmt.Println("Please provide a positive number.")
				break
			}

			for i := 0; i < count; i++ {
				logMsg := gamelogic.GetMaliciousLog()
				gl := gamelogic.GameLog{
					CurrentTime: time.Now(),
					Message:     logMsg,
					Username:    username,
				}
				key := fmt.Sprintf("%s.%s", routing.GameLogSlug, username)
				if err := pubsub.PublishGob(ch, routing.ExchangePerilTopic, key, gl); err != nil {
					fmt.Printf("Failed to publish log #%d: %s\n", i+1, err)
					break
				}
			}
			fmt.Printf("Published %d malicious logs.\n", count)

		case "quit":
			gamelogic.PrintQuit()
			os.Exit(0)
		default:
			fmt.Println("Unknown command. Type 'help' for available commands.")
		}
	}
}
