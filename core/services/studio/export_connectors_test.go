package studio

// RepliesActiveForTest reports whether a reply to a connector message is
// still being delivered. A chat turn that has ended is not enough to say
// that a connector is idle: the reply flag clears a moment after the turn
// ends, and a command that comes in that gap is refused as busy.
func (c *Connectors) RepliesActiveForTest() bool {
	c.smu.Lock()
	defer c.smu.Unlock()
	for _, st := range c.senders {
		if st.active {
			return true
		}
	}
	return false
}

// HoldJoiningMessagesForTest runs f for every message that joins a running
// reply, after the reply has been told and before the message reaches the
// chat. It sets the hook before any message comes in.
func (c *Connectors) HoldJoiningMessagesForTest(f func()) { c.beforeJoin = f }
