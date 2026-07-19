# geoip-spoa

[![codecov](https://codecov.io/gh/andrewheberle/geoip-spoa/graph/badge.svg?token=4y31WEx0JP)](https://codecov.io/gh/andrewheberle/geoip-spoa)

This is a Stream Processing Offload Agent (SPOA) for use with HAProxy to
perform GeoIP lookups of requests so access or routing decisions can be
made based on ASN, Organisation, City, Country and/or Continent.

## GeoIP Databases

The agent uses the GeoLite2 ASN and City databases in MMDB format from MaxMind
so these need to be available and kept updated for accuracy.

On Debian/Ubuntu install the following package:

```sh
apt install geoipupdate
```

Once installed add your account credentials to `/etc/GeoIP.conf` (this requires
registering an account with MaxMind).

It is important to ensure your use of the GeoLite2 databases is within what is
allowed under the EULA on MaxMind's website
[here](https://www.maxmind.com/en/geolite2/eula).

Any database in MMDB format is supported so there is no reason this SPOA could
not be used with commercially licensed GeoIP database products or databases
generated with custom data, however neither of these options have been tested.

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

### Returned Variables

The following variables are returned by the agent to HAProxy:

* txn.PREFIX.asn (integer) - The AS Number of the source IP
* txn.PREFIX.org (string) - The AS Organisation of the source IP
* txn.PREFIX.city (string) - The City of the source IP
* txn.PREFIX.country (string) - The ISO code of the country of the source IP
* txn.PREFIX.continent (string) - The ISO code of the continent of the source IP

**Note:** If a lookup returns no data because the IP address was not found in
one of the databases, this is not considered an error, but instead one or more
of the above values returned to HAProxy may be blank.

## Configuratuon

The agent can be configured via a combination of command line flags, a
configuration file and environment variables.

Options are loaded in the following order, with the ability to override
options from lower levels:

1. Defaults
2. Configuration file
3. Environment variables
4. Command line flags


### Command Line

The following command line options are supported:

| Option         | Type       | Default                             | Description                                        |
|----------------|------------|-------------------------------------|----------------------------------------------------|
| cache.size     | `int`      | `1024`                              | Number of IP lookups to cache (0 to disable)       |
| cache.ttl      | `duration` | `0`                                 | TTL for caching of IP lookups (0 to never expire)  |
| config         | `string`   |                                     | Path to YAML configuration file                    |
| db.asn         | `string`   | `/var/lib/GeoIP/GeoLite2-ASN.mmdb`  | GeoLite2 ASN database path                         |
| db.city        | `string`   | `/var/lib/GeoIP/GeoLite2-City.mmdb` | GeoLite2 City database path                        |
| debug          | `boolean`  | `false`                             | Enable debug logging                               |
| interval       | `duration` | `24h`                               | Interval between checks for new GeoLite2 databases |
| listen         | `string`   | `127.0.0.1:3000`                    | SPOA listen address                                |
| locale         | `string`   | `en`                                | Locale for City names                              |
| metrics.listen | `string`   |                                     | Listen address for Prometheus metrics              |
| metrics.path   | `string`   | `/metrics`                          | Path for Prometheus metrics                        |
| version        | `boolean`  | `false`                             | Show version and exit                              |

### Environment

All of the above command-line options can be provided as environemnt
variables as follows:

```sh
# Disable cache and enable metrics on port 9200
GEOIP_CACHE_SIZE="0" GEOIP_METRICS_LISTEN=":9200" geoip-spoa
```

### Configuration File

A YAML based configuration can be loaded via the `--config` option:

```yaml
cache:
  size: 0
db:
  asn: /var/lib/GeoIP/GeoLite2-ASN.mmdb
  city: /var/lib/GeoIP/GeoLite2-City.mmdb
debug: false
metrics:
  listen: ':9200'
```

## Caching

Lookup responses from the GeoLite2 ASN and City databases are
cached in memory by default with lookup results cached until they are evicted
under LRU pressure or cleared on a reload of either database.

Setting `cache.size` to zero disables caching.

Based on repeated lookups of a single IP address caching provides close to a
10x performance boost.

```
cpu: 13th Gen Intel(R) Core(TM) i5-1335U
BenchmarkLookup_Uncached-12    	 1207038	       971.6 ns/op	     744 B/op	       9 allocs/op
BenchmarkLookup_Cached-12      	11428749	       105.6 ns/op	       8 B/op	       1 allocs/op
```
