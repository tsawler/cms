package dialect

import "time"

// SetLockPoll shortens the advisory lock's polling round for a test, and
// returns the function that puts it back.
func SetLockPoll(d time.Duration) (restore func()) {
	was := lockPoll
	lockPoll = d
	return func() { lockPoll = was }
}
