package graphql_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IodeSystems/graphql-go/v2"
)

// Cancelling the context must let the subscription producer exit and close
// its result channel even when nobody is reading it. A subscriber that
// unsubscribes stops reading; a producer blocked on an unguarded send then
// leaks forever (upstream graphql-go/graphql#758).
func TestSubscribe_ProducerExitsOnCancelWithoutReceiver(t *testing.T) {
	t.Run("event from source channel", func(t *testing.T) {
		// Unbuffered: once the send below returns, the producer holds the
		// event and is on its way to the result channel.
		source := make(chan interface{})
		schema := makeSubscriptionSchema(t, graphql.ObjectConfig{
			Name: "Subscription",
			Fields: graphql.Fields{
				"tick": &graphql.Field{
					Type: graphql.Int,
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						return p.Source, nil
					},
					Subscribe: func(p graphql.ResolveParams) (interface{}, error) {
						return source, nil
					},
				},
			},
		})
		ctx, cancel := context.WithCancel(context.Background())
		results := graphql.Subscribe(graphql.Params{
			Schema:        schema,
			RequestString: `subscription { tick }`,
			Context:       ctx,
		})
		source <- 42
		cancel()
		assertClosedWithoutReceiving(t, results)
	})

	t.Run("single non-channel result", func(t *testing.T) {
		assertCancelledSubscribeCloses(t, func(p graphql.ResolveParams) (interface{}, error) {
			return "once", nil
		})
	})

	t.Run("subscribe resolver error", func(t *testing.T) {
		assertCancelledSubscribeCloses(t, func(p graphql.ResolveParams) (interface{}, error) {
			return nil, errors.New("refused")
		})
	})
}

// assertCancelledSubscribeCloses subscribes with an already-cancelled context,
// so the producer's one and only send happens with no receiver.
func assertCancelledSubscribeCloses(t *testing.T, subscribe graphql.FieldResolveFn) {
	t.Helper()
	schema := makeSubscriptionSchema(t, graphql.ObjectConfig{
		Name: "Subscription",
		Fields: graphql.Fields{
			"value": &graphql.Field{
				Type:      graphql.String,
				Subscribe: subscribe,
			},
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results := graphql.Subscribe(graphql.Params{
		Schema:        schema,
		RequestString: `subscription { value }`,
		Context:       ctx,
	})
	assertClosedWithoutReceiving(t, results)
}

// assertClosedWithoutReceiving gives the producer time to reach its send with
// no receiver, then reads once. A fixed producer has already exited and
// closed the channel; a leaking one is still blocked and hands over a result.
func assertClosedWithoutReceiving(t *testing.T, results chan *graphql.Result) {
	t.Helper()
	time.Sleep(200 * time.Millisecond)
	select {
	case res, ok := <-results:
		if ok {
			t.Fatalf("producer was still blocked sending after cancel, got %+v", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("result channel still open after cancel: producer goroutine leaked")
	}
}
