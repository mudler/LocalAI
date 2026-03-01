package behavior

import (
	"context"
	"testing"
	"time"
)

func TestCompositeNodeSequence(t *testing.T) {
	// Create a sequence that succeeds
	action1 := NewActionNode("Action1", func(ctx context.Context) NodeStatus {
		return StatusSuccess
	})
	action2 := NewActionNode("Action2", func(ctx context.Context) NodeStatus {
		return StatusSuccess
	})
	
	seq := NewSequenceNode("TestSequence", action1, action2)
	ctx := context.Background()
	
	status := seq.Execute(ctx)
	if status != StatusSuccess {
		t.Errorf("Expected success, got %s", status)
	}
}

func TestCompositeNodeSequenceFails(t *testing.T) {
	// Create a sequence that fails on first child
	action1 := NewActionNode("Action1", func(ctx context.Context) NodeStatus {
		return StatusFailure
	})
	action2 := NewActionNode("Action2", func(ctx context.Context) NodeStatus {
		return StatusSuccess
	})
	
	seq := NewSequenceNode("TestSequence", action1, action2)
	ctx := context.Background()
	
	status := seq.Execute(ctx)
	if status != StatusFailure {
		t.Errorf("Expected failure, got %s", status)
	}
}

func TestConditionNode(t *testing.T) {
	condition := NewConditionNode("TrueCondition", func(ctx context.Context) (bool, error) {
		return true, nil
	})
	
	ctx := context.Background()
	status := condition.Execute(ctx)
	
	if status != StatusSuccess {
		t.Errorf("Expected success for true condition, got %s", status)
	}
	
	if !condition.lastResult {
		t.Error("Expected lastResult to be true")
	}
}

func TestConditionNodeFalse(t *testing.T) {
	condition := NewConditionNode("FalseCondition", func(ctx context.Context) (bool, error) {
		return false, nil
	})
	
	ctx := context.Background()
	status := condition.Execute(ctx)
	
	if status != StatusFailure {
		t.Errorf("Expected failure for false condition, got %s", status)
	}
}

func TestConditionNodeError(t *testing.T) {
	condition := NewConditionNode("ErrorCondition", func(ctx context.Context) (bool, error) {
		return false, &testError{"test error"}
	})
	
	ctx := context.Background()
	status := condition.Execute(ctx)
	
	if status != StatusError {
		t.Errorf("Expected error status, got %s", status)
	}
}

type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}

func TestTreeTick(t *testing.T) {
	action := NewActionNode("TestAction", func(ctx context.Context) NodeStatus {
		return StatusSuccess
	})
	
	tree := NewTree("TestTree", action)
	ctx := context.Background()
	
	if !tree.LastTick().IsZero() {
		t.Error("Expected lastTick to be zero before first tick")
	}
	
	status := tree.Tick(ctx)
	
	if status != StatusSuccess {
		t.Errorf("Expected success, got %s", status)
	}
	
	if tree.LastTick().IsZero() {
		t.Error("Expected lastTick to be set after tick")
	}
	
	if tree.LastStatus() != StatusSuccess {
		t.Errorf("Expected lastStatus to be success, got %s", tree.LastStatus())
	}
}

func TestSchedulerRegistration(t *testing.T) {
	scheduler := NewScheduler(time.Hour)
	
	if scheduler.IsRunning() {
		t.Error("Expected scheduler to not be running initially")
	}
	
	action := NewActionNode("TestAction", func(ctx context.Context) NodeStatus {
		return StatusSuccess
	})
	tree := NewTree("TestTree", action)
	
	scheduler.RegisterTree(tree)
	scheduler.Start(context.Background())
	
	if !scheduler.IsRunning() {
		t.Error("Expected scheduler to be running after Start")
	}
	
	scheduler.Stop()
	
	if scheduler.IsRunning() {
		t.Error("Expected scheduler to not be running after Stop")
	}
}

func TestSchedulerTick(t *testing.T) {
	scheduler := NewScheduler(time.Hour)
	
	action := NewActionNode("TestAction", func(ctx context.Context) NodeStatus {
		return StatusSuccess
	})
	tree := NewTree("TestTree", action)
	
	scheduler.RegisterTree(tree)
	scheduler.Start(context.Background())
	
	// Trigger immediate tick
	scheduler.Trigger(context.Background())
	time.Sleep(100 * time.Millisecond)
	
	if scheduler.RunCount() < 1 {
		t.Errorf("Expected at least 1 run, got %d", scheduler.RunCount())
	}
	
	scheduler.Stop()
}

func TestCalendarStore(t *testing.T) {
	store := NewCalendarStore(24 * time.Hour)
	
	now := time.Now()
	event := &CalendarEvent{
		ID:        "event1",
		Title:     "Test Event",
		StartTime: now.Add(1 * time.Hour),
		EndTime:   now.Add(2 * time.Hour),
	}
	
	store.AddEvent(event)
	
	events := store.GetEventsInWindow()
	if len(events) != 1 {
		t.Errorf("Expected 1 event in window, got %d", len(events))
	}
	
	unnotified := store.GetUnnotifiedEvents()
	if len(unnotified) != 1 {
		t.Errorf("Expected 1 unnotified event, got %d", len(unnotified))
	}
	
	store.MarkNotified("event1")
	
	unnotified = store.GetUnnotifiedEvents()
	if len(unnotified) != 0 {
		t.Errorf("Expected 0 unnotified events after marking, got %d", len(unnotified))
	}
}

func TestCalendarBehaviorTree(t *testing.T) {
	store := NewCalendarStore(24 * time.Hour)
	
	// Add an event in the window
	now := time.Now()
	event := &CalendarEvent{
		ID:        "event1",
		Title:     "Test Meeting",
		StartTime: now.Add(1 * time.Hour),
		EndTime:   now.Add(2 * time.Hour),
	}
	store.AddEvent(event)
	
	tree := BuildCalendarBehaviorTree(store)
	
	// Build context with calendar store
	ctx := WithCalendarStore(context.Background(), store)
	ctx = WithNotifications(ctx, NewNotificationContext())
	
	status := tree.Tick(ctx)
	
	// Should succeed - there are events and they haven't been notified
	if status != StatusSuccess {
		t.Errorf("Expected success, got %s", status)
	}
	
	// Now all events should be marked as notified
	unnotified := store.GetUnnotifiedEvents()
	if len(unnotified) != 0 {
		t.Errorf("Expected 0 unnotified events after tree execution, got %d", len(unnotified))
	}
}

func TestJSONTreeParsing(t *testing.T) {
	config := JSONTreeConfig{
		Name:     "test",
		NodeType: "sequence",
		NodeName: "Root",
		Children: []JSONTreeConfig{
			{
				NodeType: "condition",
				NodeName: "Check Events",
				Condition: "check_events",
			},
			{
				NodeType: "action",
				NodeName: "Notify",
				Action:   "send_notification",
			},
		},
	}
	
	node, err := ParseJSONTree(config)
	if err != nil {
		t.Fatalf("Failed to parse JSON tree: %v", err)
	}
	
	if node.Name() != "Root" {
		t.Errorf("Expected root name 'Root', got '%s'", node.Name())
	}
}

// TestGetUnnotifiedEventsInWindow tests the new method
func TestGetUnnotifiedEventsInWindow(t *testing.T) {
	store := NewCalendarStore(24 * time.Hour)
	
	now := time.Now()
	
	// Add event in window, not notified
	event1 := &CalendarEvent{
		ID:        "event1",
		Title:     "Test Meeting 1",
		StartTime: now.Add(1 * time.Hour),
		EndTime:   now.Add(2 * time.Hour),
		Notified:  false,
	}
	
	// Add event in window, already notified
	event2 := &CalendarEvent{
		ID:        "event2",
		Title:     "Test Meeting 2",
		StartTime: now.Add(3 * time.Hour),
		EndTime:   now.Add(4 * time.Hour),
		Notified:  true,
	}
	
	// Add event outside window
	event3 := &CalendarEvent{
		ID:        "event3",
		Title:     "Test Meeting 3",
		StartTime: now.Add(25 * time.Hour),
		EndTime:   now.Add(26 * time.Hour),
		Notified:  false,
	}
	
	store.AddEvent(event1)
	store.AddEvent(event2)
	store.AddEvent(event3)
	
	// Test GetEventsInWindow - should return 2 events (event1 and event2)
	eventsInWindow := store.GetEventsInWindow()
	if len(eventsInWindow) != 2 {
		t.Errorf("Expected 2 events in window, got %d", len(eventsInWindow))
	}
	
	// Test GetUnnotifiedEvents - returns ALL unnotified events (event1 and event3)
	unnotified := store.GetUnnotifiedEvents()
	if len(unnotified) != 2 {
		t.Errorf("Expected 2 unnotified events (in and out of window), got %d", len(unnotified))
	}
}
