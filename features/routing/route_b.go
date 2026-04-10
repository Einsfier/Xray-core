package routing

// RouteB is an extended Route with balancer tag information.
type RouteB interface {
	Route
	GetBalancerTag() string
}

// RouterB is an extended Router with PickRouteB capability.
type RouterB interface {
	Router
	PickRouteB(ctx Context) (RouteB, error)
}
