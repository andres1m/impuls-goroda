package maxbot

import (
	"errors"
	"net/url"

	"github.com/google/uuid"
)

func (c *Client) OwnerRouteURL(routeID uuid.UUID) (string, error) {
	if c == nil || c.username == "" || routeID == uuid.Nil {
		return "", errors.New("invalid owner route link")
	}
	link := url.URL{Scheme: "https", Host: "max.ru", Path: "/" + c.username}
	link.RawQuery = url.Values{"startapp": {"route_" + routeID.String()}}.Encode()
	return link.String(), nil
}
