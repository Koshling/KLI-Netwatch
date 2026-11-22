package dashboard

import (
	"context"
	"fmt"
	"time"

	"netwatch/internal/event"
)

// Log time per real second. Keep the profiles in increasing order.
var replaySpeeds = [...]int{1, 5, 20, 100}

// Multipliers for the normal globe rotation step at the same frame rate.
var replayGlobeSpeeds = [...]int{1, 2, 4, 8, 16}

type replayClock struct {
	end             time.Time
	anchor          time.Time
	wall            time.Time
	speedIndex      int
	globeSpeedIndex int
	paused          bool
	sourceDone      bool
	start           time.Time
	seeking         bool
	target          time.Time
	skipped         int
	scan            func(context.Context, func(event.AccessEvent) error) (int, error)
}

func RunReplay(ctx context.Context, scan func(context.Context, func(event.AccessEvent) error) (int, error), options GeoOptions, start, end time.Time) error {
	clock := &replayClock{start: start, anchor: start, end: end, wall: time.Now(), scan: scan}
	return run(ctx, nil, options, clock)
}

type scanResult struct {
	skipped int
	err     error
}
type replayStream struct {
	events   <-chan event.AccessEvent
	result   scanResult
	cancel   context.CancelFunc
	finished chan struct{}
}

func (c *replayClock) openStream(parent context.Context) *replayStream {
	ctx, cancel := context.WithCancel(parent)
	events := make(chan event.AccessEvent)
	stream := &replayStream{events: events, cancel: cancel, finished: make(chan struct{})}
	go func() {
		defer close(stream.finished)
		defer close(events)
		stream.result.skipped, stream.result.err = c.scan(ctx, func(e event.AccessEvent) error {
			select {
			case events <- e:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	return stream
}

func (s *replayStream) close() { s.cancel(); <-s.finished }

func (c *replayClock) seekTarget(position time.Time, step time.Duration) time.Time {
	if c.seeking {
		position = c.target
	}
	target := position.Add(step)
	if target.Before(c.start) {
		return c.start
	}
	if target.After(c.end) {
		return c.end
	}
	return target
}

// The first event after target belongs to normal playback and stays pending.
func (d *Dashboard) catchUp(c *replayClock, pending *event.AccessEvent, now time.Time) bool {
	if pending != nil && !pending.Time.After(c.target) {
		d.advanceReplayRate(pending.Time)
		d.add(*pending)
		return true
	}
	if pending != nil || c.sourceDone {
		d.advanceReplayRate(c.target)
		c.anchor, c.wall, c.seeking = c.target, now, false
	}
	return false
}

func (c *replayClock) speed() int {
	return replaySpeeds[c.speedIndex]
}

func (c *replayClock) globeSpeed() int {
	return replayGlobeSpeeds[c.globeSpeedIndex]
}

func (c *replayClock) position(now time.Time) time.Time {
	if c.paused {
		return c.anchor
	}
	elapsed := now.Sub(c.wall)
	remaining := c.end.Sub(c.anchor)
	if elapsed > remaining/time.Duration(c.speed()) {
		return c.end
	}
	return c.anchor.Add(elapsed * time.Duration(c.speed()))
}

func (c *replayClock) toggle(now time.Time) {
	c.anchor = c.position(now)
	c.wall = now
	c.paused = !c.paused
}

func (c *replayClock) changeSpeed(now time.Time, step int) {
	c.globeSpeedIndex = min(max(c.globeSpeedIndex+step, 0), len(replayGlobeSpeeds)-1)
	next := min(max(c.speedIndex+step, 0), len(replaySpeeds)-1)
	if next == c.speedIndex {
		return
	}
	c.anchor = c.position(now)
	c.wall = now
	c.speedIndex = next
}

func (c *replayClock) status(processed time.Time) string {
	if c.seeking {
		return fmt.Sprintf("REPLAY SEEK %s -> %s", processed.Format("15:04:05"), c.target.Format("15:04:05"))
	}
	state := "PLAY"
	if c.paused {
		state = "PAUSE"
	}
	if c.sourceDone && !processed.Before(c.end.Truncate(time.Second)) && !c.position(time.Now()).Before(c.end) {
		state = "END"
	}
	return fmt.Sprintf("REPLAY %s %s %dx [SPACE] pause [+/-] speed | skipped %d", state, processed.Format("15:04:05"), c.speed(), c.skipped)
}

func (d *Dashboard) advanceReplayRate(now time.Time) {
	second := now.Truncate(time.Second)
	if d.currentSecond.IsZero() {
		d.currentSecond = second
		return
	}
	if !second.After(d.currentSecond) {
		return
	}
	elapsed := int64(second.Sub(d.currentSecond) / time.Second)
	if elapsed > int64(len(d.rates)) {
		clear(d.rates)
	} else {
		steps := int(elapsed)
		copy(d.rates, d.rates[steps:])
		clear(d.rates[len(d.rates)-steps:])
		d.rates[len(d.rates)-steps] = d.currentCount
	}
	d.currentCount = 0
	d.currentSecond = second
}
