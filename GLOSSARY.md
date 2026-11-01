# Glossary

**Location**: the geographic and network facts about one IP address that the agent returns to HAProxy: AS number, AS organisation, continent code, country ISO code and city name (in the configured locale). A value that could not be found is left empty; an IP address missing from the GeoLite2 databases is not an error.
_Avoid_: lookup result, GeoInfo.

**Locale**: the language used for city names, such as `en` or `de`. Set once for the whole agent. If a city has no name in the locale, the city is returned empty, with no fallback to another language.
