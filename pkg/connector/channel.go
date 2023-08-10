package connector

import (
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
)

var channelResourceTypeID = "channel"

var channelResourceType = &v2.ResourceType{
	Id:          channelResourceTypeID,
	DisplayName: "Channel",
}
