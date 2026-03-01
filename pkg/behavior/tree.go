package behavior

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// NodeStatus represents the status of a behavior tree node
type NodeStatus string

const (
	StatusSuccess NodeStatus = "success"
	StatusFailure NodeStatus = "failure"
	StatusRunning NodeStatus = "running"
	StatusError   NodeStatus = "error"
)

// NodeType represents the type of behavior tree node
type NodeType string

const (
	TypeComposite NodeType = "composite"
	TypeAction    NodeType = "action"
	TypeCondition NodeType = "condition"
)

// CompositeType represents the type of composite node
type CompositeType string

const (
	TypeSequence CompositeType = "sequence"
	TypeSelector CompositeType = "selector"
)

// Node represents a behavior tree node
type Node interface {
	Type() NodeType
	Execute(ctx context.Context) NodeStatus
	Name() string
}

// CompositeNode is a node that has children
type CompositeNode struct {
	name         string
	children     []Node
	nodeType     NodeType
	compositeType CompositeType
}

func NewSequenceNode(name string, children ...Node) *CompositeNode {
	return &CompositeNode{
		name:          name,
		children:      children,
		nodeType:      TypeComposite,
		compositeType: TypeSequence,
	}
}

func NewSelectorNode(name string, children ...Node) *CompositeNode {
	return &CompositeNode{
		name:          name,
		children:      children,
		nodeType:      TypeComposite,
		compositeType: TypeSelector,
	}
}

func (c *CompositeNode) Type() NodeType {
	return c.nodeType
}

func (c *CompositeNode) Name() string {
	return c.name
}

func (c *CompositeNode) AddChild(child Node) {
	c.children = append(c.children, child)
}

// Execute runs children based on composite type
func (c *CompositeNode) Execute(ctx context.Context) NodeStatus {
	if c.compositeType == TypeSelector {
		return c.SelectorExecute(ctx)
	}
	return c.SequenceExecute(ctx)
}

// SequenceExecute runs all children in sequence (AND logic)
func (c *CompositeNode) SequenceExecute(ctx context.Context) NodeStatus {
	for _, child := range c.children {
		status := child.Execute(ctx)
		if status != StatusSuccess {
			return status
		}
	}
	return StatusSuccess
}

// SelectorExecute runs children until one succeeds (OR logic)
func (c *CompositeNode) SelectorExecute(ctx context.Context) NodeStatus {
	for _, child := range c.children {
		status := child.Execute(ctx)
		if status == StatusSuccess {
			return StatusSuccess
		}
		if status == StatusRunning {
			return StatusRunning
		}
	}
	return StatusFailure
}

// ActionNode is a leaf node that performs an action
type ActionNode struct {
	name      string
	action    func(ctx context.Context) NodeStatus
	actionCtx context.Context
}

func NewActionNode(name string, action func(ctx context.Context) NodeStatus) *ActionNode {
	return &ActionNode{
		name:   name,
		action: action,
	}
}

func (a *ActionNode) Type() NodeType {
	return TypeAction
}

func (a *ActionNode) Name() string {
	return a.name
}

func (a *ActionNode) Execute(ctx context.Context) NodeStatus {
	return a.action(ctx)
}

// ConditionNode is a leaf node that evaluates a condition
type ConditionNode struct {
	name       string
	condition  func(ctx context.Context) (bool, error)
	lastResult bool
}

func NewConditionNode(name string, condition func(ctx context.Context) (bool, error)) *ConditionNode {
	return &ConditionNode{
		name:      name,
		condition: condition,
	}
}

func (c *ConditionNode) Type() NodeType {
	return TypeCondition
}

func (c *ConditionNode) Name() string {
	return c.name
}

func (c *ConditionNode) Execute(ctx context.Context) NodeStatus {
	result, err := c.condition(ctx)
	if err != nil {
		return StatusError
	}
	c.lastResult = result
	if result {
		return StatusSuccess
	}
	return StatusFailure
}

// Tree represents a behavior tree
type Tree struct {
	root      Node
	name      string
	mu        sync.RWMutex
	lastTick  time.Time
	lastStatus NodeStatus
}

func NewTree(name string, root Node) *Tree {
	return &Tree{
		name: name,
		root: root,
	}
}

func (t *Tree) Tick(ctx context.Context) NodeStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	t.lastTick = time.Now()
	t.lastStatus = t.root.Execute(ctx)
	return t.lastStatus
}

func (t *Tree) LastTick() time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastTick
}

func (t *Tree) LastStatus() NodeStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastStatus
}

func (t *Tree) Root() Node {
	return t.root
}

func (t *Tree) String() string {
	return fmt.Sprintf("Tree: %s, LastTick: %s, Status: %s", t.name, t.lastTick, t.lastStatus)
}
