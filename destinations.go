// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

// Resolution inspects metadata only. This interface intentionally has no join
// or send operation: saving a destination cannot change group membership.
type destinationClient interface {
	GetGroupInfoFromLink(context.Context, string) (*types.GroupInfo, error)
	GetGroupInfo(context.Context, types.JID) (*types.GroupInfo, error)
	IsOnWhatsApp(context.Context, []string) ([]types.IsOnWhatsAppResponse, error)
}

var inviteCodePattern = regexp.MustCompile(`^[A-Za-z0-9]{22}$`)
var groupIDPattern = regexp.MustCompile(`^[1-9][0-9]*(?:-[0-9]+)?@g\.us$`)

func groupInviteCode(input string) (string, bool) {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(strings.ToLower(input), "chat.whatsapp.com/") {
		input = "https://" + input
	}
	link, err := url.Parse(input)
	if err != nil || link.Scheme != "https" || !strings.EqualFold(link.Host, "chat.whatsapp.com") || link.User != nil || link.Fragment != "" {
		return "", false
	}
	// Sharing parameters are not part of the invite code. Never pass them to
	// WhatsApp, follow redirects or accept lookalike hosts/extra path segments.
	code := strings.TrimSuffix(strings.TrimPrefix(link.EscapedPath(), "/"), "/")
	return code, inviteCodePattern.MatchString(code)
}

func resolveDestination(ctx context.Context, client destinationClient, input string, phone, lid types.JID) (destination, error) {
	input = strings.TrimSpace(input)
	if code, ok := groupInviteCode(input); ok {
		preview, err := client.GetGroupInfoFromLink(ctx, code)
		if err != nil {
			return destination{}, destinationError(err)
		}
		if preview == nil || !groupIDPattern.MatchString(preview.JID.String()) {
			return destination{}, errors.New("invalid_destination")
		}
		// Invite previews may be visible to non-members and omit participants.
		// Fetch current member metadata before accepting the destination.
		return resolveGroup(ctx, client, preview.JID, phone, lid)
	}
	if groupIDPattern.MatchString(input) {
		jid, _ := types.ParseJID(input)
		return resolveGroup(ctx, client, jid, phone, lid)
	}
	if strings.HasSuffix(input, "@"+types.DefaultUserServer) {
		input = strings.TrimSuffix(input, "@"+types.DefaultUserServer)
	}
	number := strings.NewReplacer(" ", "", "(", "", ")", "", "+", "", "-", "", "\t", "", "\n", "").Replace(input)
	if !phonePattern.MatchString(number) {
		return destination{}, errors.New("invalid_destination")
	}
	users, err := client.IsOnWhatsApp(ctx, []string{number})
	if err != nil {
		return destination{}, err
	}
	for _, user := range users {
		if user.IsIn && !user.JID.IsEmpty() {
			return destination{user.JID.ToNonAD().String(), "+" + number, false}, nil
		}
	}
	return destination{}, errors.New("invalid_destination")
}

func resolveGroup(ctx context.Context, client destinationClient, jid, phone, lid types.JID) (destination, error) {
	group, err := client.GetGroupInfo(ctx, jid)
	if err != nil {
		return destination{}, destinationError(err)
	}
	if group == nil || group.JID != jid || strings.TrimSpace(group.Name) == "" {
		return destination{}, errors.New("invalid_destination")
	}
	if !groupSendable(group, phone, lid) {
		return destination{}, errors.New("permission")
	}
	return destination{group.JID.String(), group.Name, true}, nil
}

func destinationError(err error) error {
	switch {
	case errors.Is(err, whatsmeow.ErrInviteLinkInvalid), errors.Is(err, whatsmeow.ErrInviteLinkRevoked), errors.Is(err, whatsmeow.ErrGroupNotFound):
		return errors.New("invalid_destination")
	case errors.Is(err, whatsmeow.ErrNotInGroup):
		return errors.New("permission")
	default:
		return err
	}
}
