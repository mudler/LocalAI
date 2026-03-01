package behavior

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// CalendarEvent represents a calendar event
type CalendarEvent struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
	Description string    `json:"description"`
	Attendees   []string  `json:"attendees"`
	Notified    bool      `json:"notified"`
	NotifiedAt  time.Time `json:"notified_at"`
}

// CalendarStore stores calendar events and notification state
type CalendarStore struct {
	mu       sync.RWMutex
	events   map[string]*CalendarEvent
	window   time.Duration
}

// NewCalendarStore creates a new calendar store
func NewCalendarStore(window time.Duration) *CalendarStore {
	return &CalendarStore{
		events: make(map[string]*CalendarEvent),
		window: window,
	}
}

// AddEvent adds or updates a calendar event
func (c *CalendarStore) AddEvent(event *CalendarEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events[event.ID] = event
}

// GetEventsInWindow returns events within the time window from now
func (c *CalendarStore) GetEventsInWindow() []*CalendarEvent {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	now := time.Now()
	windowEnd := now.Add(c.window)
	
	var result []*CalendarEvent
	for _, event := range c.events {
		if event.StartTime.After(now) && event.StartTime.Before(windowEnd) {
			result = append(result, event)
		}
	}
	return result
}

// GetUnnotifiedEvents returns ALL events that haven't been notified yet (regardless of window)
func (c *CalendarStore) GetUnnotifiedEvents() []*CalendarEvent {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	var result []*CalendarEvent
	for _, event := range c.events {
		if !event.Notified {
			result = append(result, event)
		}
	}
	return result
}

// GetUnnotifiedEventsInWindow returns unnotified events within the time window
func (c *CalendarStore) GetUnnotifiedEventsInWindow() []*CalendarEvent {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	now := time.Now()
	windowEnd := now.Add(c.window)
	
	var result []*CalendarEvent
	for _, event := range c.events {
		if !event.Notified && event.StartTime.After(now) && event.StartTime.Before(windowEnd) {
			result = append(result, event)
		}
	}
	return result
}

// MarkNotified marks an event as notified
func (c *CalendarStore) MarkNotified(eventID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if event, ok := c.events[eventID]; ok {
		event.Notified = true
		event.NotifiedAt = time.Now()
	}
}

// SetWindow updates the time window
func (c *CalendarStore) SetWindow(window time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.window = window
}

// Window returns the current time window
func (c *CalendarStore) Window() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.window
}

// CalendarContextKey is the key for calendar data in context
type CalendarContextKey string

const (
	CalendarStoreKey    CalendarContextKey = "calendar_store"
	NotificationKey     CalendarContextKey = "notifications"
	TimeWindowKey       CalendarContextKey = "time_window"
)

// CalendarContext returns the calendar store from context
func CalendarContext(ctx context.Context) (*CalendarStore, bool) {
	store, ok := ctx.Value(CalendarStoreKey).(*CalendarStore)
	return store, ok
}

// NotificationContext manages notifications in context
type NotificationContext struct {
	mu           sync.Mutex
	notifications []string
}

func NewNotificationContext() *NotificationContext {
	return &NotificationContext{
		notifications: make([]string, 0),
	}
}

func (n *NotificationContext) Add(notification string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.notifications = append(n.notifications, notification)
}

func (n *NotificationContext) GetAll() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	result := make([]string, len(n.notifications))
	copy(result, n.notifications)
	return result
}

// WithCalendarStore adds a calendar store to context
func WithCalendarStore(ctx context.Context, store *CalendarStore) context.Context {
	return context.WithValue(ctx, CalendarStoreKey, store)
}

// WithNotifications adds a notification context to context
func WithNotifications(ctx context.Context, notif *NotificationContext) context.Context {
	return context.WithValue(ctx, NotificationKey, notif)
}

// CheckCalendarEventsCondition checks if there are calendar events in the window
func CheckCalendarEventsCondition(ctx context.Context) (bool, error) {
	store, ok := ctx.Value(CalendarStoreKey).(*CalendarStore)
	if !ok {
		return false, fmt.Errorf("calendar store not found in context")
	}
	
	events := store.GetEventsInWindow()
	return len(events) > 0, nil
}

// CheckEventsNeedNotificationCondition checks if there are events that need notification
// (i.e., unnotified events in the window) - this is the positive version that returns 
// success when notification IS needed
func CheckEventsNeedNotificationCondition(ctx context.Context) (bool, error) {
	store, ok := ctx.Value(CalendarStoreKey).(*CalendarStore)
	if !ok {
		return false, fmt.Errorf("calendar store not found in context")
	}
	
	unnotified := store.GetUnnotifiedEventsInWindow()
	return len(unnotified) > 0, nil
}

// SendNotificationAction sends a notification about calendar events
func SendNotificationAction(ctx context.Context) NodeStatus {
	store, ok := ctx.Value(CalendarStoreKey).(*CalendarStore)
	if !ok {
		return StatusError
	}
	
	notifCtx, ok := ctx.Value(NotificationKey).(*NotificationContext)
	if !ok {
		notifCtx = NewNotificationContext()
	}
	
	// Get unnotified events IN the window
	events := store.GetUnnotifiedEventsInWindow()
	if len(events) == 0 {
		return StatusSuccess
	}
	
	for _, event := range events {
		notification := fmt.Sprintf("Calendar Update: %s at %s", event.Title, event.StartTime.Format(time.RFC3339))
		notifCtx.Add(notification)
		fmt.Printf("[Notification] %s\n", notification)
	}
	
	return StatusSuccess
}

// MarkEventsNotifiedAction marks all events in the window as notified
func MarkEventsNotifiedAction(ctx context.Context) NodeStatus {
	store, ok := ctx.Value(CalendarStoreKey).(*CalendarStore)
	if !ok {
		return StatusError
	}
	
	// Mark only events in the window as notified
	events := store.GetUnnotifiedEventsInWindow()
	for _, event := range events {
		store.MarkNotified(event.ID)
		fmt.Printf("[Calendar] Marked event %s as notified\n", event.ID)
	}
	
	return StatusSuccess
}

// BuildCalendarBehaviorTree creates a behavior tree for calendar status verification
// The tree structure uses a selector at the root to handle both cases:
// 1. If events exist in window AND need notification -> notify and mark
// 2. If no events in window or already notified -> return success
func BuildCalendarBehaviorTree(store *CalendarStore) *Tree {
	// Condition: Are there events in the next 24 hours?
	checkEventsCondition := NewConditionNode("Check: Events in Next 24h?", CheckCalendarEventsCondition)
	
	// Condition: Are there events that NEED notification?
	checkNeedsNotificationCondition := NewConditionNode("Check: Needs Notification?", CheckEventsNeedNotificationCondition)
	
	// Action: Send notification
	sendNotificationAction := NewActionNode("Action: Send Update Notification", SendNotificationAction)
	
	// Action: Mark events as notified
	markNotifiedAction := NewActionNode("Action: Mark Events as Notified", MarkEventsNotifiedAction)
	
	// Inner sequence: send notification and mark as notified
	notificationSequence := NewSequenceNode("Notify Sequence",
		sendNotificationAction,
		markNotifiedAction,
	)
	
	// Mid-level sequence: check events exist -> if yes, check needs notification -> if yes, run notification
	eventsCheckSequence := NewSequenceNode("Events Check Sequence",
		checkEventsCondition,
		checkNeedsNotificationCondition,
		notificationSequence,
	)
	
	// No events action - returns success when there are no events
	noEventsAction := NewActionNode("Action: No Events - Success", func(ctx context.Context) NodeStatus {
		return StatusSuccess
	})
	
	// Root selector: try to check and notify events, or succeed if no events
	rootSelector := NewSelectorNode("Root Selector",
		eventsCheckSequence,
		noEventsAction,
	)
	
	tree := NewTree("CalendarStatusVerification", rootSelector)
	_ = store // store is used via context
	
	return tree
}

// JSONTreeConfig represents a behavior tree in JSON format
type JSONTreeConfig struct {
	Name      string           `json:"name"`
	NodeType  string           `json:"node_type"`
	NodeName  string           `json:"node_name"`
	Children  []JSONTreeConfig `json:"children,omitempty"`
	Condition string           `json:"condition,omitempty"`
	Action    string           `json:"action,omitempty"`
}

// ParseJSONTree builds a tree from JSON configuration
func ParseJSONTree(config JSONTreeConfig) (Node, error) {
	switch config.NodeType {
	case "sequence":
		children := make([]Node, 0, len(config.Children))
		for _, childConfig := range config.Children {
			child, err := ParseJSONTree(childConfig)
			if err != nil {
				return nil, err
			}
			children = append(children, child)
		}
		return NewSequenceNode(config.NodeName, children...), nil
		
	case "selector":
		children := make([]Node, 0, len(config.Children))
		for _, childConfig := range config.Children {
			child, err := ParseJSONTree(childConfig)
			if err != nil {
				return nil, err
			}
			children = append(children, child)
		}
		return NewSelectorNode(config.NodeName, children...), nil
		
	case "condition":
		var conditionFunc func(ctx context.Context) (bool, error)
		switch config.Condition {
		case "check_events":
			conditionFunc = CheckCalendarEventsCondition
		case "check_notified":
			conditionFunc = CheckEventsNeedNotificationCondition
		default:
			conditionFunc = func(ctx context.Context) (bool, error) {
				return false, fmt.Errorf("unknown condition: %s", config.Condition)
			}
		}
		return NewConditionNode(config.NodeName, conditionFunc), nil
		
	case "action":
		var actionFunc func(ctx context.Context) NodeStatus
		switch config.Action {
		case "send_notification":
			actionFunc = SendNotificationAction
		case "mark_notified":
			actionFunc = MarkEventsNotifiedAction
		default:
			actionFunc = func(ctx context.Context) NodeStatus {
				return StatusError
			}
		}
		return NewActionNode(config.NodeName, actionFunc), nil
		
	default:
		return nil, fmt.Errorf("unknown node type: %s", config.NodeType)
	}
}

// TreeToJSON converts a tree to JSON configuration
func TreeToJSON(tree *Tree) (JSONTreeConfig, error) {
	return treeToJSONRecursive(tree.Root())
}

func treeToJSONRecursive(node Node) (JSONTreeConfig, error) {
	config := JSONTreeConfig{
		NodeName: node.Name(),
		NodeType: string(node.Type()),
	}
	
	// Handle composite nodes
	if composite, ok := node.(*CompositeNode); ok {
		children := make([]JSONTreeConfig, 0, len(composite.children))
		for _, child := range composite.children {
			childConfig, err := treeToJSONRecursive(child)
			if err != nil {
				return JSONTreeConfig{}, err
			}
			children = append(children, childConfig)
		}
		config.Children = children
	}
	
	// Handle condition nodes
	if condition, ok := node.(*ConditionNode); ok {
		_ = condition
		// Would need to serialize the condition function reference
	}
	
	return config, nil
}

// MarshalJSON implements JSON marshaling for CalendarEvent
func (e CalendarEvent) MarshalJSON() ([]byte, error) {
	type Alias CalendarEvent
	return json.Marshal(&struct {
		Alias
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
		NotifiedAt string `json:"notified_at"`
	}{
		Alias:      Alias(e),
		StartTime:  e.StartTime.Format(time.RFC3339),
		EndTime:    e.EndTime.Format(time.RFC3339),
		NotifiedAt: e.NotifiedAt.Format(time.RFC3339),
	})
}
