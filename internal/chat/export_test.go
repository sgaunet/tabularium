package chat

import "time"

// This file exposes three internals to the black-box test in package chat_test.
// Each is here because provoking the behaviour through the public API alone
// would be slower or less exact, not because the test wants a shortcut:
//
//   - the retry classifier has a dozen interesting inputs that are far cheaper
//     to enumerate than to provoke from a server;
//   - the backoff ceiling is the one part of a jittered wait that is exactly
//     assertable;
//   - the backoff base has to be shortened, because a test that spends seven
//     seconds asleep proves nothing that a millisecond does not.

// Retryable is the retry classifier.
var Retryable = retryable

// BackoffCeiling is the upper bound of the wait before a given attempt.
var BackoffCeiling = backoffCeiling

// SetBackoffBase shortens the backoff schedule for a test.
func SetBackoffBase(c *Client, d time.Duration) { c.backoffBase = d }
