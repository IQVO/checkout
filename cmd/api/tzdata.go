package main

// The zone database is embedded so the IANA time zone a requoted offer names
// resolves on any base image, not only one that ships tzdata (ADR 0008).
import _ "time/tzdata"
