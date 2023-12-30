package googlecloud_test

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"cloud.google.com/go/pubsub"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/tests"

	"github.com/ThreeDotsLabs/watermill-googlecloud/pkg/googlecloud"
)

// Run `docker-compose up` and set PUBSUB_EMULATOR_HOST=localhost:8085 for this to work

func newPubSub(t *testing.T, enableMessageOrdering bool, marshaler googlecloud.Marshaler, unmarshaler googlecloud.Unmarshaler, subscriptionName googlecloud.SubscriptionNameFn) (message.Publisher, message.Subscriber) {
	logger := watermill.NewStdLogger(true, true)

	publisher, err := googlecloud.NewPublisher(
		googlecloud.PublisherConfig{
			EnableMessageOrdering: enableMessageOrdering,
			Marshaler:             marshaler,
		},
		logger,
	)
	require.NoError(t, err)

	subscriber, err := googlecloud.NewSubscriber(
		googlecloud.SubscriberConfig{
			GenerateSubscriptionName: subscriptionName,
			SubscriptionConfig: pubsub.SubscriptionConfig{
				RetainAckedMessages:   false,
				EnableMessageOrdering: enableMessageOrdering,
			},
			Unmarshaler: unmarshaler,
		},
		logger,
	)
	require.NoError(t, err)

	return publisher, subscriber
}

func createPubSub(t *testing.T) (message.Publisher, message.Subscriber) {
	var defaultMarshalerUnmarshaler googlecloud.DefaultMarshalerUnmarshaler
	return newPubSub(t, false, defaultMarshalerUnmarshaler, defaultMarshalerUnmarshaler, googlecloud.TopicSubscriptionName)
}

func createPubSubWithSubscriptionName(t *testing.T, subscriptionName string) (message.Publisher, message.Subscriber) {
	var defaultMarshalerUnmarshaler googlecloud.DefaultMarshalerUnmarshaler
	return newPubSub(t, false, defaultMarshalerUnmarshaler, defaultMarshalerUnmarshaler,
		googlecloud.TopicSubscriptionNameWithSuffix(subscriptionName),
	)
}

func createPubSubWithOrdering(t *testing.T) (message.Publisher, message.Subscriber) {
	return newPubSub(
		t,
		true,
		googlecloud.NewOrderingMarshaler(func(topic string, msg *message.Message) (string, error) {
			return "ordering_key", nil
		}),
		googlecloud.NewOrderingUnmarshaler(func(orderingKey string, msg *message.Message) error {
			return nil
		}),
		googlecloud.TopicSubscriptionName,
	)
}

func createPubSubWithSubscriptionNameWithOrdering(t *testing.T, subscriptionName string) (message.Publisher, message.Subscriber) {
	return newPubSub(
		t,
		true,
		googlecloud.NewOrderingMarshaler(func(topic string, msg *message.Message) (string, error) {
			return "ordering_key", nil
		}),
		googlecloud.NewOrderingUnmarshaler(func(orderingKey string, msg *message.Message) error {
			return nil
		}),
		googlecloud.TopicSubscriptionNameWithSuffix(subscriptionName),
	)
}

func TestPublishSubscribe(t *testing.T) {
	tests.TestPubSub(
		t,
		tests.Features{
			ConsumerGroups:      true,
			ExactlyOnceDelivery: false,
			GuaranteedOrder:     false,
			Persistent:          true,
		},
		createPubSub,
		createPubSubWithSubscriptionName,
	)
}

func TestPublishSubscribeOrdering(t *testing.T) {
	t.Skip("skipping because the emulator does not currently redeliver nacked messages when ordering is enabled")

	if testing.Short() {
		t.Skip("skipping long tests")
	}

	tests.TestPubSub(
		t,
		tests.Features{
			ConsumerGroups:      true,
			ExactlyOnceDelivery: false,
			GuaranteedOrder:     true,
			Persistent:          true,
		},
		createPubSubWithOrdering,
		createPubSubWithSubscriptionNameWithOrdering,
	)
}

func TestSubscriberUnexpectedTopicForSubscription(t *testing.T) {
	rand.Seed(time.Now().Unix())
	testNumber := rand.Int()
	logger := watermill.NewStdLogger(true, true)

	subNameFn := func(topic string) string {
		return fmt.Sprintf("sub_%d", testNumber)
	}

	sub1, err := googlecloud.NewSubscriber(googlecloud.SubscriberConfig{
		GenerateSubscriptionName: subNameFn,
	}, logger)
	require.NoError(t, err)

	topic1 := fmt.Sprintf("topic1_%d", testNumber)

	sub2, err := googlecloud.NewSubscriber(googlecloud.SubscriberConfig{
		GenerateSubscriptionName: subNameFn,
	}, logger)
	require.NoError(t, err)
	topic2 := fmt.Sprintf("topic2_%d", testNumber)

	howManyMessages := 100

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	messagesTopic1, err := sub1.Subscribe(ctx, topic1)
	require.NoError(t, err)

	allMessagesReceived := make(chan struct{})
	go func() {
		defer close(allMessagesReceived)
		messagesReceived := 0
		for range messagesTopic1 {
			messagesReceived++
			if messagesReceived == howManyMessages {
				return
			}
		}
	}()

	produceMessages(t, topic1, howManyMessages)

	select {
	case <-allMessagesReceived:
		t.Log("All topic 1 messages received")
	case <-ctx.Done():
		t.Fatal("Test timed out")
	}

	_, err = sub2.Subscribe(ctx, topic2)
	require.Equal(t, googlecloud.ErrUnexpectedTopic, errors.Cause(err))
}

func produceMessages(t *testing.T, topic string, howMany int) {
	pub, err := googlecloud.NewPublisher(googlecloud.PublisherConfig{}, nil)
	require.NoError(t, err)
	defer pub.Close()

	messages := make([]*message.Message, howMany)
	for i := 0; i < howMany; i++ {
		messages[i] = message.NewMessage(watermill.NewUUID(), []byte{})
	}

	require.NoError(t, pub.Publish(topic, messages...))
}

func TestSubscriberEndpointChanged(t *testing.T) {
	rand.Seed(time.Now().Unix())
	testNumber := rand.Int()
	logger := watermill.NewStdLogger(true, true)

	subNameFn := func(topic string) string {
		return fmt.Sprintf("sub_%d", testNumber)
	}

	topic := fmt.Sprintf("topic2_%d", testNumber)

	sub1, err := googlecloud.NewSubscriber(googlecloud.SubscriberConfig{
		GenerateSubscriptionName: subNameFn,
		SubscriptionConfig: pubsub.SubscriptionConfig{
			PushConfig: pubsub.PushConfig{
				Endpoint: "https://google.com/v1",
				Attributes: map[string]string{
					"test_key": "test_value",
				},
				AuthenticationMethod: nil,
			},
			AckDeadline:         30 * time.Second,
			RetainAckedMessages: false,
			RetentionDuration:   0,
			ExpirationPolicy:    time.Duration(0),
			Labels: map[string]string{
				"project": "test",
			},
			EnableMessageOrdering: true,
			Filter:                "",
			RetryPolicy: &pubsub.RetryPolicy{
				MinimumBackoff: 10 * time.Second,
				MaximumBackoff: 30 * time.Second,
			},
			Detached:                      false,
			TopicMessageRetentionDuration: 0,
		},
	}, logger)
	require.NoError(t, err)

	err = sub1.SubscribeInitialize(topic)
	require.NoError(t, err)

	sub2, err := googlecloud.NewSubscriber(googlecloud.SubscriberConfig{
		GenerateSubscriptionName: subNameFn,
		SubscriptionConfig: pubsub.SubscriptionConfig{
			PushConfig: pubsub.PushConfig{
				Endpoint: "https://google.com/v2",
				Attributes: map[string]string{
					"test_key": "test_value",
				},
			},
		},
	}, logger)
	require.NoError(t, err)

	err = sub2.SubscribeInitialize(topic)
	require.NoError(t, err)
}
