package server

import (
	"net/url"
	"strings"
)

func requestLogURI(raw string) (string, bool) {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil {
		return "[invalid-uri]", true
	}
	segments := strings.Split(parsed.Path, "/")
	for index, segment := range segments {
		if segment != "shared-routes" {
			continue
		}
		path := "/shared-routes/[redacted]"
		if index == 3 && segments[0] == "" && segments[1] == "api" && segments[2] == "v1" {
			path = "/api/v1" + path
		}
		remaining := segments[index+1:]
		if len(remaining) == 2 && remaining[0] != "" && remaining[1] == "copy" {
			path += "/copy"
		}
		return path, true
	}
	return raw, false
}
