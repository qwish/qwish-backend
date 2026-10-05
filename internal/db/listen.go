package db

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"log"
	"time"
)

// Listen reserves a session connection. Use a direct/session pooler URL, never
// a transaction pooler. onConnect clears caches after a missed notification.
func Listen(ctx context.Context, pool *pgxpool.Pool, channel string, onConnect func(), receive func(context.Context, string)) {
	for ctx.Err() == nil {
		conn, err := pool.Acquire(ctx)
		if err == nil {
			// Channels are constants owned by callers, not user input.
			_, err = conn.Exec(ctx, "LISTEN "+channel)
			if err == nil {
				if onConnect != nil {
					onConnect()
				}
				for ctx.Err() == nil {
					var payload string
					n, e := conn.Conn().WaitForNotification(ctx)
					if e != nil {
						err = e
						break
					}
					payload = n.Payload
					receive(ctx, payload)
				}
			}
			// A listening connection must never be returned to the reusable pool.
			_ = conn.Conn().Close(context.Background())
			conn.Release()
		}
		if ctx.Err() != nil {
			return
		}
		log.Printf("listen %s disconnected: %v", channel, err)
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
