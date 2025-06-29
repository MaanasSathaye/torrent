package cstate

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"time"
)

type logger interface {
	Println(v ...interface{})
	Printf(format string, v ...interface{})
	Print(v ...interface{})
}
type Shared struct {
	done context.CancelCauseFunc
	log  logger
}

type T interface {
	Update(context.Context, *Shared) T
}

func Idle(next T, t time.Duration, cond *sync.Cond, signals ...*sync.Cond) idle {
	return idle{
		timeout: t,
		next:    next,
		cond:    cond,
		signals: signals,
	}
}

type idle struct {
	timeout time.Duration
	next    T
	cond    *sync.Cond
	signals []*sync.Cond
}

func (t idle) monitor(ctx context.Context, target *sync.Cond, signals ...*sync.Cond) {
	ctx, done := context.WithCancel(ctx)
	defer func() {
		for _, s := range signals {
			s.Broadcast()
		}
	}()
	defer done()

	for _, s := range signals {
		go func() {
			for {
				s.L.Lock()
				s.Wait()
				s.L.Unlock()
				target.Broadcast()
				select {
				case <-ctx.Done():
					return
				default:
				}
			}
		}()
	}

	if t.timeout > 0 {
		go func() {
			select {
			case <-time.After(t.timeout):
				t.cond.Broadcast()
			case <-ctx.Done():
			}
		}()
	}

	target.L.Lock()
	target.Wait()
	target.L.Unlock()
}

func (t idle) Update(ctx context.Context, c *Shared) T {
	t.monitor(ctx, t.cond, t.signals...)
	return t.next
}

func (t idle) String() string {
	return fmt.Sprintf("%T - %s - idle", t.next, t.next)
}

func Failure(cause error) failed {
	return failed{cause: cause}
}

type failed struct {
	cause error
}

func (t failed) Update(ctx context.Context, c *Shared) T {
	c.done(t.cause)
	return nil
}

func (t failed) String() string {
	return fmt.Sprintf("%T - %s", t, t.cause)
}

func Warning(next T, cause error) warning {
	return warning{next: next, cause: cause}
}

type warning struct {
	cause error
	next  T
}

func (t warning) Update(ctx context.Context, c *Shared) T {
	c.log.Println("[warning]", t.cause)
	return t.next
}

func (t warning) String() string {
	return fmt.Sprintf("%T - %T", t, t.cause)
}

func Fn(fn fn) fn {
	return fn
}

type fn func(context.Context, *Shared) T

func (t fn) Update(ctx context.Context, s *Shared) T {
	return t(ctx, s)
}

func (t fn) String() string {
	pc := reflect.ValueOf(t).Pointer()
	info := runtime.FuncForPC(pc)
	fname, line := info.FileLine(pc)
	return fmt.Sprintf("%s:%d", fname, line)
}

func Run(ctx context.Context, s T, l logger) error {
	ctx, cancelled := context.WithCancelCause(ctx)
	var (
		m = Shared{
			done: cancelled,
			log:  l,
		}
	)

	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		default:
			l.Printf("%T %s\n", s, s)
			s = s.Update(ctx, &m)
		}

		if s == nil {
			return context.Cause(ctx)
		}
	}
}
