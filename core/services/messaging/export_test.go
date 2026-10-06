package messaging

// NATSRouteForTest exposes the route table so the external specs can pin the
// queue group per kind, which no producer-side behaviour reveals.
var NATSRouteForTest = natsRoute
