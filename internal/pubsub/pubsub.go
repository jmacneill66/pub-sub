package pubsub

import (
	"bytes"
	"encoding/gob"
	"context"
	"encoding/json"
	"fmt"
	"github.com/bootdotdev/learn-pub-sub-starter/internal/routing"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	QueueTypeDurable = iota
	QueueTypeTransient
)

// PublishJSON publishes a generic value to RabbitMQ as a JSON message.
func PublishJSON[T any](ch *amqp.Channel, exchange, key string, val T) error {
	body, err := json.Marshal(val)
	if err != nil {
		return err
	}
	return ch.PublishWithContext(context.Background(), exchange, key, false, false, amqp.Publishing{
		ContentType: "application/json",
		Body:        body,
	})
}

func PublishGob[T any](ch *amqp.Channel, exchange, key string, val T) error {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(val); err != nil {
		return err
	}
	return ch.PublishWithContext(
		context.Background(),
		exchange,
		key,
		false,
		false,
		amqp.Publishing{
			ContentType: "application/gob",
			Body:        buf.Bytes(),
		},
	)
}

// DeclareAndBind declares a queue and binds it to an exchange.
func DeclareAndBind(
	conn *amqp.Connection,
	exchange,
	queueName,
	key string,
	simpleQueueType int,
) (*amqp.Channel, amqp.Queue, error) {

	ch, err := conn.Channel()
	if err != nil {
		return nil, amqp.Queue{}, fmt.Errorf("channel creation error: %w", err)
	}

	durable := simpleQueueType == QueueTypeDurable
	autoDelete := simpleQueueType == QueueTypeTransient
	exclusive := simpleQueueType == QueueTypeTransient

	// Special case for game_logs queue
	var args amqp.Table
	if queueName != "game_logs" {
		args = amqp.Table{
			"x-dead-letter-exchange": routing.ExchangePerilDead,
		}
	}

	q, err := ch.QueueDeclare(
		queueName,
		durable,
		autoDelete,
		exclusive,
		false,
		args,
	)
	if err != nil {
		return nil, amqp.Queue{}, fmt.Errorf("queue declare error: %w", err)
	}

	if err := ch.QueueBind(
		q.Name,
		key,
		exchange,
		false,
		nil,
	); err != nil {
		return nil, amqp.Queue{}, fmt.Errorf("queue bind error: %w", err)
	}

	return ch, q, nil
}


func SubscribeJSON[T any](
	conn *amqp.Connection,
	exchange,
	queueName,
	key string,
	simpleQueueType int,
	handler func(T) AckType,
) error {
	ch, queue, err := DeclareAndBind(conn, exchange, queueName, key, simpleQueueType)
	if err != nil {
		return err
	}

	deliveries, err := ch.Consume(
		queue.Name,
		"",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return err
	}

	go func() {
		for msg := range deliveries {
			var val T
			if err := json.Unmarshal(msg.Body, &val); err != nil {
				fmt.Printf("Failed to unmarshal message: %s\n", err)
				_ = msg.Nack(false, false)
				continue
			}

			switch handler(val) {
			case Ack:
				_ = msg.Ack(false)
				fmt.Println("[pubsub] Acked message")
			case NackRequeue:
				_ = msg.Nack(false, true)
				fmt.Println("[pubsub] Nack with requeue")
			case NackDiscard:
				_ = msg.Nack(false, false)
				fmt.Println("[pubsub] Nack with discard")
			}
		}
	}()

	return nil
}

func subscribe[T any](
	conn *amqp.Connection,
	exchange,
	queueName,
	key string,
	simpleQueueType int,
	handler func(T) AckType,
	unmarshaller func([]byte) (T, error),
) error {
	ch, queue, err := DeclareAndBind(conn, exchange, queueName, key, simpleQueueType)
	if err != nil {
		return fmt.Errorf("failed to declare and bind: %w", err)
	}
	// Set QoS to limit prefetch to 10 messages
	if err := ch.Qos(10, 0, false); err != nil {
		return fmt.Errorf("failed to set QoS: %w", err)
	}

	deliveries, err := ch.Consume(
		queue.Name,
		"",
		false, // auto-ack
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return err
	}

	go func() {
		for msg := range deliveries {
			val, err := unmarshaller(msg.Body)
			if err != nil {
				fmt.Printf("Failed to decode message: %s\n", err)
				_ = msg.Nack(false, false)
				continue
			}

			switch handler(val) {
			case Ack:
				_ = msg.Ack(false)
				fmt.Println("[pubsub] Acked message")
			case NackRequeue:
				_ = msg.Nack(false, true)
				fmt.Println("[pubsub] Nack with requeue")
			case NackDiscard:
				_ = msg.Nack(false, false)
				fmt.Println("[pubsub] Nack with discard")
			}
		}
	}()

	return nil
}

func SubscribeGob[T any](
	conn *amqp.Connection,
	exchange,
	queueName,
	key string,
	simpleQueueType int,
	handler func(T) AckType,
) error {
	unmarshaller := func(data []byte) (T, error) {
		var val T
		err := gob.NewDecoder(bytes.NewReader(data)).Decode(&val)
		return val, err
	}
	return subscribe(conn, exchange, queueName, key, simpleQueueType, handler, unmarshaller)
}

