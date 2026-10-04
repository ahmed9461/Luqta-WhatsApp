// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"go.mau.fi/whatsmeow/types"
	"testing"
)

func TestGroupMembershipAndPostingPermissions(t *testing.T) {
	phone := types.NewJID("19990000000", types.DefaultUserServer)
	lid := types.NewJID("11111", types.HiddenUserServer)
	for _, test := range []struct {
		name                              string
		participant                       types.GroupParticipant
		announce, suspended, parent, want bool
	}{
		{"phone member", types.GroupParticipant{JID: phone}, false, false, false, true},
		{"LID member", types.GroupParticipant{JID: lid}, false, false, false, true},
		{"phone mapped", types.GroupParticipant{PhoneNumber: phone}, false, false, false, true},
		{"announcement member", types.GroupParticipant{JID: lid}, true, false, false, false},
		{"announcement admin", types.GroupParticipant{JID: lid, IsAdmin: true}, true, false, false, true},
		{"announcement owner", types.GroupParticipant{JID: lid, IsSuperAdmin: true}, true, false, false, true},
		{"not member", types.GroupParticipant{}, false, false, false, false},
		{"paused", types.GroupParticipant{JID: phone}, false, true, false, false},
		{"community parent", types.GroupParticipant{JID: phone}, false, false, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			group := &types.GroupInfo{Participants: []types.GroupParticipant{test.participant}, Suspended: test.suspended}
			group.IsAnnounce = test.announce
			group.IsParent = test.parent
			if groupSendable(group, phone, lid) != test.want {
				t.Fatal("permission mismatch")
			}
		})
	}
}
