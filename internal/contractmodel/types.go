package contractmodel

type Control string

const (
	ControlDeclared               Control = "declared"
	ControlSecret                 Control = "secret"
	ControlDerived                Control = "derived"
	ControlFixed                  Control = "fixed"
	ControlExternallySynchronized Control = "externally-synchronized"
	ControlUnmanaged              Control = "unmanaged"
)

type Lifecycle string

const (
	LifecycleRender      Lifecycle = "render"
	LifecycleInitialize  Lifecycle = "initialize"
	LifecycleReconcile   Lifecycle = "reconcile"
	LifecycleSynchronize Lifecycle = "synchronize"
	LifecycleVerify      Lifecycle = "verify"
	LifecyclePreserve    Lifecycle = "preserve"
)

type Status string

const StatusImplemented Status = "implemented"
