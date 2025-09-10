package fsm

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
)

type Event interface {
	Id() string
}

type State[E Event] interface {
	Transition(E) (State[E], error)
	String() string
}

type Engine[E Event] interface {
	Receive(context.Context) (E, error)
	RestoreState(context.Context, E) (State[E], error)
	SaveState(context.Context, State[E]) error
}

func StartExecution[E Event](ctx context.Context, en Engine[E]) error {
	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("stop engine execution due to context being done")
			return ctx.Err()
		default:
			event, err := en.Receive(ctx)
			if err != nil {
				return fmt.Errorf("abort execution, failed to receive event, error: %w", err)
			}

			state, err := en.RestoreState(ctx, event)
			if err != nil {
				log.Err(err).Str("event_id", event.Id()).Msg("failed to restore state from event")
				continue
			}

			if newState, err := state.Transition(event); err != nil {
				log.Err(err).Str("state", state.String()).Str("event_id", event.Id()).Msgf("state transition failed")
				continue
			} else if err = en.SaveState(ctx, newState); err != nil {
				log.Err(err).Str("new_state", newState.String()).Msgf("failed to save new state after transition")
				continue
			}
		}
	}
}
