# geoip-spoa

This is a Stream Processing Offload Engine (SPOA) for use with HAProxy to
perform GeoIP lookups of requests.

## HAProxy Integration

Add the config to you HAProxy configuration:

```
# An example HTTP frontend
frontend fe_http
	mode http

	acl allowed_asn var(txn.geoip.asn) -m int 12345

	filter spoe engine geoip config /etc/haproxy/spoe.cfg

	http-request send-spoe-group geoip lookup
	http-request reject unless allowed_asn

	# the rest of your frontend config is unchanged

# An example TCP frontend
frontend fe_tcp
	mode tcp

	acl allowed_asn var(txn.geoip.asn) -m int 12345

	filter spoe engine geoip config /etc/haproxy/spoe.cfg

	tcp-request inspect-delay 500ms
	tcp-request content send-spoe-group geoip lookup
    tcp-request content reject unless allowed_asn

	# the rest of your frontend config is unchanged


# This is the backend for communication with the agent
backend be_spoe
	timeout connect 5s
	timeout server  5m

	server spoa 127.0.0.1:3000 check
```

A dedicated SPOE configuration file must be created:

```
[geoip]

spoe-agent geoip
    log global

    timeout hello      2s
    timeout processing 100ms
    timeout idle       3m

	option var-prefix geoip

    groups lookup

	# This must match the backend name in the main config
    use-backend be_spoe

spoe-message geoip-lookup
	# The source IP is passed to the SPOA
    args ip=src

spoe-group lookup
    messages geoip-lookup
```
