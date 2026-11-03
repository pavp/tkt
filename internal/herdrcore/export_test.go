package herdrcore

import "time"

// SetQuestionAfter shortens how long StartAgent waits for a first status before calling the agent not ready.
func SetQuestionAfter(c *Client, d time.Duration) { c.questionAfter = d }
