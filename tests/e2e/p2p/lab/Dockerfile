# Image for every node of the P2P NAT lab: the NAT routers need iptables and
# conntrack, the peers only need a shell. The lab binary is mounted at run time.
FROM alpine:3.20
RUN apk add --no-cache iptables iptables-legacy iproute2 conntrack-tools
