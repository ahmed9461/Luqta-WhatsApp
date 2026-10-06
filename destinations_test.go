// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

const testInviteCode = "AbCdEfGhIjKlMnOpQrStUv"

func TestGroupInviteParsing(t *testing.T) {
	for _, input := range []string{
		"https://chat.whatsapp.com/" + testInviteCode,
		" https://chat.whatsapp.com/" + testInviteCode + "?s=cl&p=a&mode=share ",
		"chat.whatsapp.com/" + testInviteCode + "/?mode=share",
		"https://CHAT.WHATSAPP.COM/" + testInviteCode,
	} {
		code, ok := groupInviteCode(input)
		if !ok || code != testInviteCode {
			t.Fatal("valid shared invite was not normalized")
		}
	}
	for _, input := range []string{
		"https://chat.whatsapp.com.evil.example/" + testInviteCode,
		"https://evil.example/chat.whatsapp.com/" + testInviteCode,
		"https://chat.whatsapp.com@evil.example/" + testInviteCode,
		"https://user@chat.whatsapp.com/" + testInviteCode,
		"https://chat.whatsapp.com:443/" + testInviteCode,
		"http://chat.whatsapp.com/" + testInviteCode,
		"https://chat.whatsapp.com/" + testInviteCode + "/extra",
		"https://chat.whatsapp.com/" + testInviteCode + "#fragment",
		"https://chat.whatsapp.com/short",
		"https://chat.whatsapp.com/AbCdEfGhIjKlMnOpQrStU_",
		"https://chat.whatsapp.com/%41bCdEfGhIjKlMnOpQrStUv",
		"https://chat.whatsapp.com/?code=" + testInviteCode,
	} {
		if _, ok := groupInviteCode(input); ok {
			t.Fatal("invalid or lookalike invite accepted")
		}
		client := newDestinationFixture()
		if _, err := resolveDestination(context.Background(), client, input, client.phone, client.lid); category(err) != "invalid_destination" {
			t.Fatal("invalid input did not fail locally")
		}
		if client.linkCalls+client.groupCalls+client.phoneCalls != 0 {
			t.Fatal("invalid input reached provider")
		}
	}
}

func TestInviteResolutionUsesCurrentMembershipAndSendPermission(t *testing.T) {
	for _, test := range []struct {
		name, want       string
		member, announce bool
		admin, suspended bool
		parent           bool
	}{
		{"member", "", true, false, false, false, false},
		{"nonmember preview", "permission", false, false, false, false, false},
		{"announcement member", "permission", true, true, false, false, false},
		{"announcement admin", "", true, true, true, false, false},
		{"suspended", "permission", true, false, false, true, false},
		{"community parent", "permission", true, false, false, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newDestinationFixture()
			client.group.Participants = nil
			if test.member {
				client.group.Participants = []types.GroupParticipant{{JID: client.lid, IsAdmin: test.admin}}
			}
			client.group.IsAnnounce, client.group.Suspended, client.group.IsParent = test.announce, test.suspended, test.parent
			result, err := resolveDestination(context.Background(), client, "https://chat.whatsapp.com/"+testInviteCode+"?s=cl&p=a", client.phone, client.lid)
			if test.want != "" {
				if err == nil || category(err) != test.want {
					t.Fatal("unauthorized destination was not refused")
				}
			} else if err != nil || result.ID != client.group.JID.String() || result.Title != "current synthetic title" || !result.Group {
				t.Fatal("canonical member destination was not returned")
			}
			if client.code != testInviteCode || client.linkCalls != 1 || client.groupCalls != 1 || client.phoneCalls != 0 {
				t.Fatal("invite resolution used unexpected provider operations")
			}
		})
	}
}

func TestSelectedGroupIsRevalidatedWithoutInviteLookup(t *testing.T) {
	client := newDestinationFixture()
	result, err := resolveDestination(context.Background(), client, client.group.JID.String(), client.phone, client.lid)
	if err != nil || result.ID != client.group.JID.String() || client.linkCalls != 0 || client.groupCalls != 1 {
		t.Fatal("selected group did not receive a fresh permission check")
	}
	client.group.IsAnnounce = true
	if _, err = resolveDestination(context.Background(), client, result.ID, client.phone, client.lid); err == nil || category(err) != "permission" {
		t.Fatal("changed posting permission was ignored")
	}
}

func TestDestinationFailuresRetainActionableCategories(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{whatsmeow.ErrInviteLinkInvalid, "invalid_destination"},
		{whatsmeow.ErrInviteLinkRevoked, "invalid_destination"},
		{whatsmeow.ErrGroupNotFound, "invalid_destination"},
		{whatsmeow.ErrNotInGroup, "permission"},
		{context.DeadlineExceeded, "network"},
	} {
		client := newDestinationFixture()
		client.linkError = fmt.Errorf("provider: %w", test.err)
		_, err := resolveDestination(context.Background(), client, "https://chat.whatsapp.com/"+testInviteCode, client.phone, client.lid)
		if err == nil || category(err) != test.want || client.groupCalls != 0 {
			t.Fatal("provider error was misclassified")
		}
	}
	client := newDestinationFixture()
	client.groupError = whatsmeow.ErrNotInGroup
	_, err := resolveDestination(context.Background(), client, "https://chat.whatsapp.com/"+testInviteCode, client.phone, client.lid)
	if err == nil || category(err) != "permission" {
		t.Fatal("member metadata denial was ignored")
	}
	client.groupError = nil
	client.group.JID = types.NewJID("120363999999", types.GroupServer)
	if _, err = resolveDestination(context.Background(), client, "https://chat.whatsapp.com/"+testInviteCode, client.phone, client.lid); err == nil {
		t.Fatal("mismatched group identity was accepted")
	}
}

func TestPhoneResolutionAndSavedPersonRemainSupported(t *testing.T) {
	for _, input := range []string{"+1 (999) 000-0000", "19990000000@s.whatsapp.net"} {
		client := newDestinationFixture()
		result, err := resolveDestination(context.Background(), client, input, client.phone, client.lid)
		if err != nil || result.ID != client.phone.String() || result.Group || client.phoneCalls != 1 || client.groupCalls+client.linkCalls != 0 {
			t.Fatal("phone destination regressed")
		}
	}
}

type destinationFixture struct {
	phone, lid                        types.JID
	preview, group                    *types.GroupInfo
	linkCalls, groupCalls, phoneCalls int
	code                              string
	linkError, groupError             error
}

func newDestinationFixture() *destinationFixture {
	phone := types.NewJID("19990000000", types.DefaultUserServer)
	lid := types.NewJID("11111", types.HiddenUserServer)
	jid := types.NewJID("120363123456", types.GroupServer)
	group := &types.GroupInfo{JID: jid, Participants: []types.GroupParticipant{{JID: lid}}}
	group.Name = "current synthetic title"
	return &destinationFixture{phone: phone, lid: lid, preview: &types.GroupInfo{JID: jid}, group: group}
}

func (c *destinationFixture) GetGroupInfoFromLink(_ context.Context, code string) (*types.GroupInfo, error) {
	c.linkCalls++
	c.code = code
	return c.preview, c.linkError
}
func (c *destinationFixture) GetGroupInfo(_ context.Context, jid types.JID) (*types.GroupInfo, error) {
	c.groupCalls++
	if jid != c.preview.JID {
		return nil, errors.New("invalid_destination")
	}
	return c.group, c.groupError
}
func (c *destinationFixture) IsOnWhatsApp(_ context.Context, phones []string) ([]types.IsOnWhatsAppResponse, error) {
	c.phoneCalls++
	if len(phones) != 1 || phones[0] != c.phone.User {
		return nil, errors.New("invalid_destination")
	}
	return []types.IsOnWhatsAppResponse{{JID: c.phone, IsIn: true}}, nil
}
