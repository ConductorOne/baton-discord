package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	resource_sdk "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"

	"github.com/ConductorOne/baton-discord/pkg/client"
)

const (
	//nolint:gosec // fixture value for the fake API, not a real credential
	testToken   = "test-bot-token"
	testGuildID = "700000000000000001"

	roleEveryoneID = testGuildID // Discord's @everyone role shares the guild ID.
	roleAdminsID   = "700000000000000011"
	roleManagedID  = "700000000000000012"

	channelTextID     = "700000000000000101"
	channelVoiceID    = "700000000000000102"
	channelCategoryID = "700000000000000103"
	channelThreadID   = "700000000000000104"

	userAliceID    = "800000000000000001"
	userBobID      = "800000000000000002"
	userBotID      = "800000000000000003"
	userStrangerID = "800000000000000009"

	// Repeated JSON keys and entitlement slugs, named so the fixtures and
	// assertions cannot drift apart.
	keyName          = "name"
	keyPermissions   = "permissions"
	slugSendMessages = "send_messages"
	keyCode          = "code"
	keyRoles         = "roles"
	keyJoinedAt      = "joined_at"

	// Discord's wire values for the two permission overwrite kinds.
	overwriteWireTypeRole   = 0
	overwriteWireTypeMember = 1
	testGuildName           = "Acme Guild"
	testJoinedAt            = "2024-01-02T03:04:05.000000+00:00"
)

// recordedRequest is one request the fake Discord API received.
type recordedRequest struct {
	Method string
	Path   string
	Query  url.Values
	Body   string
}

// fakeDiscord is a stand-in for the Discord REST API.
type fakeDiscord struct {
	t *testing.T

	mu       sync.Mutex
	requests []recordedRequest

	// handlers maps "METHOD /path" to a response. Paths are matched after the
	// /api/v<n> prefix is stripped.
	handlers map[string]func(w http.ResponseWriter, r *http.Request, rec recordedRequest)

	server *httptest.Server
}

func newFakeDiscord(t *testing.T) *fakeDiscord {
	t.Helper()

	fake := &fakeDiscord{
		t:        t,
		handlers: map[string]func(http.ResponseWriter, *http.Request, recordedRequest){},
	}

	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			fake.t.Errorf("reading request body: %v", err)
		}

		// Every Discord call must carry the bot-prefixed token. Asserting it
		// here means no individual test has to remember to.
		if got, want := r.Header.Get("Authorization"), "Bot "+testToken; got != want {
			fake.t.Errorf("Authorization header = %q, want %q", got, want)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		// The RoundTripper override preserves discordgo's versioned path, so
		// strip it to keep handler keys readable.
		path := r.URL.Path
		if trimmed, ok := trimAPIPrefix(path); ok {
			path = trimmed
		} else {
			fake.t.Errorf("request path %q is missing the /api/v<n> prefix", r.URL.Path)
		}

		rec := recordedRequest{
			Method: r.Method,
			Path:   path,
			Query:  r.URL.Query(),
			Body:   string(body),
		}

		fake.mu.Lock()
		fake.requests = append(fake.requests, rec)
		handler := fake.handlers[r.Method+" "+path]
		fake.mu.Unlock()

		if handler == nil {
			fake.t.Errorf("unexpected request: %s %s", r.Method, path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"unexpected request","code":0}`))
			return
		}

		handler(w, r, rec)
	}))
	t.Cleanup(fake.server.Close)

	return fake
}

// trimAPIPrefix strips discordgo's "/api/v<n>" prefix from a request path.
func trimAPIPrefix(path string) (string, bool) {
	const prefix = "/api/v"
	if !strings.HasPrefix(path, prefix) {
		return path, false
	}
	rest := path[len(prefix):]
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return "", false
	}
	return rest[slash:], true
}

func (f *fakeDiscord) handle(method, path string, handler func(http.ResponseWriter, *http.Request, recordedRequest)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[method+" "+path] = handler
}

// handleJSON registers a handler that always answers with the same JSON value.
func (f *fakeDiscord) handleJSON(method, path string, value any) {
	f.handle(method, path, func(w http.ResponseWriter, _ *http.Request, _ recordedRequest) {
		writeJSON(f.t, w, http.StatusOK, value)
	})
}

// handleStatus registers a handler that answers with a bare status and a
// Discord-shaped error body.
func (f *fakeDiscord) handleStatus(method, path string, status int, code int) {
	f.handle(method, path, func(w http.ResponseWriter, _ *http.Request, _ recordedRequest) {
		writeJSON(f.t, w, status, map[string]any{"message": http.StatusText(status), "code": code})
	})
}

func (f *fakeDiscord) requestsFor(method, path string) []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()

	var matched []recordedRequest
	for _, request := range f.requests {
		if request.Method == method && request.Path == path {
			matched = append(matched, request)
		}
	}
	return matched
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encoding response: %v", err)
	}
}

func (f *fakeDiscord) newClient(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.New(context.Background(), testToken, f.server.URL)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("client.Close: %v", err)
		}
	})
	return c
}

// afterCursor reads a paging cursor the way Discord interprets it: absent and
// zero both mean "from the beginning", because 0 sorts below every snowflake.
func afterCursor(rec recordedRequest) string {
	after := rec.Query.Get("after")
	if after == "0" {
		return ""
	}
	return after
}

// paginationToken wraps a next-page token the way the SDK hands it back in.
func paginationToken(token string) pagination.Token {
	return pagination.Token{Token: token}
}

// --- Fixtures ---------------------------------------------------------------

func memberJSON(userID, username, globalName, nick string, bot bool, roleIDs []string) map[string]any {
	user := map[string]any{
		"id":       userID,
		"username": username,
		"bot":      bot,
	}
	if globalName != "" {
		user["global_name"] = globalName
	}
	member := map[string]any{
		"user":      user,
		keyRoles:    roleIDs,
		keyJoinedAt: testJoinedAt,
	}
	if nick != "" {
		member["nick"] = nick
	}
	return member
}

func roleJSON(id, name string, managed bool, permissions discord.Permissions) map[string]any {
	return map[string]any{
		"id":           id,
		keyName:        name,
		"managed":      managed,
		keyPermissions: fmt.Sprintf("%d", permissions),
		"position":     1,
	}
}

func channelJSON(id, name string, channelType discord.ChannelType, overwrites []map[string]any) map[string]any {
	channel := map[string]any{
		"id":       id,
		keyName:    name,
		"type":     int(channelType),
		"guild_id": testGuildID,
		"position": 0,
	}
	if overwrites != nil {
		channel["permission_overwrites"] = overwrites
	}
	return channel
}

func overwriteJSON(id string, overwriteType int, allow, deny discord.Permissions) map[string]any {
	return map[string]any{
		"id":    id,
		"type":  overwriteType,
		"allow": fmt.Sprintf("%d", allow),
		"deny":  fmt.Sprintf("%d", deny),
	}
}

func guildResourceID(t *testing.T) *v2.ResourceId {
	t.Helper()
	id, err := resource_sdk.NewResourceID(guildResourceType, testGuildID)
	if err != nil {
		t.Fatalf("NewResourceID: %v", err)
	}
	return id
}

// resourceFor builds the resource a syncer would have been handed for an
// entitlements or grants call.
func resourceFor(t *testing.T, resourceType *v2.ResourceType, id, displayName string, parent *v2.ResourceId) *v2.Resource {
	t.Helper()
	opts := []resource_sdk.ResourceOption{}
	if parent != nil {
		opts = append(opts, resource_sdk.WithParentResourceID(parent))
	}
	res, err := resource_sdk.NewResource(displayName, resourceType, id, opts...)
	if err != nil {
		t.Fatalf("NewResource: %v", err)
	}
	return res
}

// --- Tests ------------------------------------------------------------------

// TestGuildListPaginates covers the cursor chain over /users/@me/guilds: a full
// page must be followed by an `after` request seeded from the last guild, and a
// short page must terminate the sync.
func TestGuildListPaginates(t *testing.T) {
	fake := newFakeDiscord(t)

	firstPage := make([]map[string]any, 0, client.GuildPageSize)
	for i := 0; i < client.GuildPageSize; i++ {
		firstPage = append(firstPage, map[string]any{
			"id":           fmt.Sprintf("9000000000000%05d", i),
			keyName:        fmt.Sprintf("Guild %d", i),
			keyPermissions: "0",
		})
	}
	lastOfFirstPage := firstPage[len(firstPage)-1]["id"].(string)

	fake.handle("GET", "/users/@me/guilds", func(w http.ResponseWriter, r *http.Request, rec recordedRequest) {
		switch afterCursor(rec) {
		case "":
			writeJSON(t, w, http.StatusOK, firstPage)
		case lastOfFirstPage:
			writeJSON(t, w, http.StatusOK, []map[string]any{
				{"id": testGuildID, keyName: testGuildName, keyPermissions: "0"},
			})
		default:
			t.Errorf("unexpected after cursor %q", afterCursor(rec))
			writeJSON(t, w, http.StatusOK, []map[string]any{})
		}
	})

	builder := newGuildBuilder(fake.newClient(t))
	ctx := context.Background()

	resources, results, err := builder.List(ctx, nil, resource_sdk.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}
	if len(resources) != client.GuildPageSize {
		t.Errorf("page 1 returned %d guilds, want %d", len(resources), client.GuildPageSize)
	}
	if results == nil || results.NextPageToken == "" {
		t.Fatal("a full page must return a next page token")
	}

	resources, results, err = builder.List(ctx, nil, resource_sdk.SyncOpAttrs{
		PageToken: paginationToken(results.NextPageToken),
	})
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}
	if len(resources) != 1 {
		t.Errorf("page 2 returned %d guilds, want 1", len(resources))
	}
	if results != nil && results.NextPageToken != "" {
		t.Errorf("a short page must end pagination, got token %q", results.NextPageToken)
	}

	requests := fake.requestsFor("GET", "/users/@me/guilds")
	if len(requests) != 2 {
		t.Fatalf("made %d guild requests, want 2", len(requests))
	}
	if got := afterCursor(requests[0]); got != "" {
		t.Errorf("first request carried a cursor %q, want none", got)
	}
	if got := afterCursor(requests[1]); got != lastOfFirstPage {
		t.Errorf("second request after=%q, want %q", got, lastOfFirstPage)
	}
}

// TestGuildResourceDeclaresChildren guards the annotations that drive the
// per-server sync of members, roles, and channels. Losing them would silently
// reduce the connector to syncing servers and nothing else.
func TestGuildResourceDeclaresChildren(t *testing.T) {
	guild, err := newGuildResource(testGuildID, testGuildName)
	if err != nil {
		t.Fatalf("newGuildResource: %v", err)
	}

	found := map[string]bool{}
	for _, annotation := range guild.GetAnnotations() {
		if !annotation.MessageIs((*v2.ChildResourceType)(nil)) {
			continue
		}
		child := &v2.ChildResourceType{}
		if err := annotation.UnmarshalTo(child); err != nil {
			t.Fatalf("unmarshalling child resource type: %v", err)
		}
		found[child.GetResourceTypeId()] = true
	}

	for _, want := range []string{userResourceTypeID, roleResourceTypeID, channelResourceTypeID} {
		if !found[want] {
			t.Errorf("guild resource does not declare %q as a child resource type", want)
		}
	}
}

// TestChildListingsRequireParent pins the guard that keeps the SDK's
// parentless pass over every resource type from double-syncing children.
func TestChildListingsRequireParent(t *testing.T) {
	fake := newFakeDiscord(t)
	discordClient := fake.newClient(t)
	ctx := context.Background()

	type lister interface {
		List(context.Context, *v2.ResourceId, resource_sdk.SyncOpAttrs) ([]*v2.Resource, *resource_sdk.SyncOpResults, error)
	}

	for name, builder := range map[string]lister{
		"user":    newUserBuilder(discordClient),
		"role":    newRoleBuilder(discordClient),
		"channel": newChannelBuilder(discordClient),
	} {
		resources, _, err := builder.List(ctx, nil, resource_sdk.SyncOpAttrs{})
		if err != nil {
			t.Errorf("%s List(nil parent): %v", name, err)
		}
		if len(resources) != 0 {
			t.Errorf("%s List(nil parent) returned %d resources, want 0", name, len(resources))
		}
	}

	// No parent means no upstream call at all.
	if len(fake.requests) != 0 {
		t.Errorf("parentless listings issued %d requests, want 0", len(fake.requests))
	}
}

// TestUserListTraits covers display-name precedence, bot classification, and
// parenting to the server the member was listed from.
func TestUserListTraits(t *testing.T) {
	fake := newFakeDiscord(t)
	fake.handleJSON("GET", "/guilds/"+testGuildID+"/members", []map[string]any{
		// Nickname wins over global name and username.
		memberJSON(userAliceID, "alice", "Alice Anderson", "Ali", false, []string{roleAdminsID}),
		// Global name wins over username when there is no nickname.
		memberJSON(userBobID, "bob", "Bobby", "", false, nil),
		// A bot member must be classified as a service account.
		memberJSON(userBotID, "helperbot", "", "", true, nil),
	})

	builder := newUserBuilder(fake.newClient(t))
	resources, results, err := builder.List(context.Background(), guildResourceID(t), resource_sdk.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if results != nil && results.NextPageToken != "" {
		t.Errorf("a short member page must end pagination, got %q", results.NextPageToken)
	}
	if len(resources) != 3 {
		t.Fatalf("got %d users, want 3", len(resources))
	}

	wantNames := map[string]string{
		userAliceID: "Ali",
		userBobID:   "Bobby",
		userBotID:   "helperbot",
	}
	wantAccountType := map[string]v2.UserTrait_AccountType{
		userAliceID: v2.UserTrait_ACCOUNT_TYPE_HUMAN,
		userBobID:   v2.UserTrait_ACCOUNT_TYPE_HUMAN,
		userBotID:   v2.UserTrait_ACCOUNT_TYPE_SERVICE,
	}

	for _, res := range resources {
		id := res.GetId().GetResource()
		if got := res.GetDisplayName(); got != wantNames[id] {
			t.Errorf("user %s display name = %q, want %q", id, got, wantNames[id])
		}
		if got := res.GetParentResourceId().GetResource(); got != testGuildID {
			t.Errorf("user %s parent = %q, want %q", id, got, testGuildID)
		}
		trait, err := resource_sdk.GetUserTrait(res)
		if err != nil {
			t.Fatalf("GetUserTrait(%s): %v", id, err)
		}
		if got := trait.GetAccountType(); got != wantAccountType[id] {
			t.Errorf("user %s account type = %v, want %v", id, got, wantAccountType[id])
		}
	}
}

// TestUserListPaginates checks the member cursor chain, which is the pagination
// this connector depends on most heavily.
func TestUserListPaginates(t *testing.T) {
	fake := newFakeDiscord(t)

	firstPage := make([]map[string]any, 0, client.MemberPageSize)
	for i := 0; i < client.MemberPageSize; i++ {
		firstPage = append(firstPage,
			memberJSON(fmt.Sprintf("9000000000000%05d", i), fmt.Sprintf("member%d", i), "", "", false, nil))
	}
	lastOfFirstPage := firstPage[len(firstPage)-1]["user"].(map[string]any)["id"].(string)

	fake.handle("GET", "/guilds/"+testGuildID+"/members", func(w http.ResponseWriter, r *http.Request, rec recordedRequest) {
		switch afterCursor(rec) {
		case "":
			writeJSON(t, w, http.StatusOK, firstPage)
		case lastOfFirstPage:
			writeJSON(t, w, http.StatusOK, []map[string]any{
				memberJSON(userAliceID, "alice", "", "", false, nil),
			})
		default:
			t.Errorf("unexpected after cursor %q", afterCursor(rec))
			writeJSON(t, w, http.StatusOK, []map[string]any{})
		}
	})

	builder := newUserBuilder(fake.newClient(t))
	ctx := context.Background()
	parent := guildResourceID(t)

	_, results, err := builder.List(ctx, parent, resource_sdk.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}
	if results == nil || results.NextPageToken == "" {
		t.Fatal("a full member page must return a next page token")
	}

	resources, results, err := builder.List(ctx, parent, resource_sdk.SyncOpAttrs{
		PageToken: paginationToken(results.NextPageToken),
	})
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}
	if len(resources) != 1 {
		t.Errorf("page 2 returned %d users, want 1", len(resources))
	}
	if results != nil && results.NextPageToken != "" {
		t.Errorf("a short page must end pagination, got %q", results.NextPageToken)
	}

	requests := fake.requestsFor("GET", "/guilds/"+testGuildID+"/members")
	if len(requests) != 2 {
		t.Fatalf("made %d member requests, want 2", len(requests))
	}
	if got := requests[0].Query.Get("limit"); got != fmt.Sprintf("%d", client.MemberPageSize) {
		t.Errorf("first member request limit=%q, want %d", got, client.MemberPageSize)
	}
	if got := afterCursor(requests[1]); got != lastOfFirstPage {
		t.Errorf("second member request after=%q, want %q", got, lastOfFirstPage)
	}
}

// TestUserListRejectsMembersWithoutUser covers the missing-intent case. Discord
// omits the user object when the Server Members Intent is off, and treating
// that as an empty member would report a populated server as having no members.
func TestUserListRejectsMembersWithoutUser(t *testing.T) {
	fake := newFakeDiscord(t)
	fake.handleJSON("GET", "/guilds/"+testGuildID+"/members", []map[string]any{
		{keyRoles: []string{}, keyJoinedAt: testJoinedAt},
	})

	builder := newUserBuilder(fake.newClient(t))
	_, _, err := builder.List(context.Background(), guildResourceID(t), resource_sdk.SyncOpAttrs{})
	if err == nil {
		t.Fatal("expected an error for a member with no user object")
	}
	if !strings.Contains(err.Error(), "Server Members Intent") {
		t.Errorf("error should name the missing intent, got: %v", err)
	}
}

// TestRoleListProfile checks the role listing, including that a role's
// server-wide permission bitmask is carried as a decimal string. Formatting it
// as a float would render large values in scientific notation, and Discord
// permissions run well past 2^31.
func TestRoleListProfile(t *testing.T) {
	fake := newFakeDiscord(t)
	const highPermissions = discord.PermissionUseEmbeddedActivities | discord.PermissionViewChannel

	fake.handleJSON("GET", "/guilds/"+testGuildID+"/roles", []map[string]any{
		roleJSON(roleEveryoneID, "@everyone", false, discord.PermissionViewChannel),
		roleJSON(roleAdminsID, "Admins", false, highPermissions),
		roleJSON(roleManagedID, "Helper Bot Role", true, 0),
	})

	builder := newRoleBuilder(fake.newClient(t))
	resources, results, err := builder.List(context.Background(), guildResourceID(t), resource_sdk.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if results != nil && results.NextPageToken != "" {
		t.Errorf("the role listing is unpaginated, got token %q", results.NextPageToken)
	}
	if len(resources) != 3 {
		t.Fatalf("got %d roles, want 3", len(resources))
	}

	byID := map[string]*v2.Resource{}
	for _, res := range resources {
		byID[res.GetId().GetResource()] = res
		if got := res.GetParentResourceId().GetResource(); got != testGuildID {
			t.Errorf("role %s parent = %q, want %q", res.GetId().GetResource(), got, testGuildID)
		}
	}

	admins, ok := byID[roleAdminsID]
	if !ok {
		t.Fatalf("Admins role missing from %v", byID)
	}
	profile := admins.GetProfile().AsMap()
	if got, want := profile[keyPermissions], fmt.Sprintf("%d", highPermissions); got != want {
		t.Errorf("Admins permissions = %v, want %q (decimal, not scientific notation)", got, want)
	}

	managed, ok := byID[roleManagedID]
	if !ok {
		t.Fatalf("managed role missing from %v", byID)
	}
	if got := managed.GetProfile().AsMap()["managed"]; got != true {
		t.Errorf("managed role profile managed = %v, want true", got)
	}
}

// TestRoleGrantsIncludeImplicitEveryone covers the two role-membership rules:
// a named role is granted only to its holders, while @everyone is held by every
// member implicitly and never appears in a member's role list.
func TestRoleGrantsIncludeImplicitEveryone(t *testing.T) {
	fake := newFakeDiscord(t)
	fake.handleJSON("GET", "/guilds/"+testGuildID+"/members", []map[string]any{
		memberJSON(userAliceID, "alice", "", "", false, []string{roleAdminsID}),
		memberJSON(userBobID, "bob", "", "", false, nil),
	})

	builder := newRoleBuilder(fake.newClient(t))
	ctx := context.Background()
	parent := guildResourceID(t)

	adminGrants, _, err := builder.Grants(ctx,
		resourceFor(t, roleResourceType, roleAdminsID, "Admins", parent), resource_sdk.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Grants(Admins): %v", err)
	}
	if len(adminGrants) != 1 {
		t.Fatalf("Admins has %d grants, want 1", len(adminGrants))
	}
	if got := adminGrants[0].GetPrincipal().GetId().GetResource(); got != userAliceID {
		t.Errorf("Admins granted to %q, want %q", got, userAliceID)
	}

	everyoneGrants, _, err := builder.Grants(ctx,
		resourceFor(t, roleResourceType, roleEveryoneID, "@everyone", parent), resource_sdk.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Grants(@everyone): %v", err)
	}
	if len(everyoneGrants) != 2 {
		t.Errorf("@everyone has %d grants, want 2 (every member holds it implicitly)", len(everyoneGrants))
	}
}

// TestRoleEntitlementIDIsStable is the regression test for the reason this
// rewrite changed slugs: an entitlement ID must not embed display text, or
// renaming the role re-identifies every entitlement and grant derived from it.
func TestRoleEntitlementIDIsStable(t *testing.T) {
	parent := guildResourceID(t)

	before := resourceFor(t, roleResourceType, roleAdminsID, "Admins", parent)
	after := resourceFor(t, roleResourceType, roleAdminsID, "Administrators", parent)

	builder := &roleBuilder{}
	beforeEnts, _, err := builder.Entitlements(context.Background(), before, resource_sdk.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Entitlements(before): %v", err)
	}
	afterEnts, _, err := builder.Entitlements(context.Background(), after, resource_sdk.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Entitlements(after): %v", err)
	}

	if beforeEnts[0].GetId() != afterEnts[0].GetId() {
		t.Errorf("renaming the role changed the entitlement ID: %q became %q",
			beforeEnts[0].GetId(), afterEnts[0].GetId())
	}
	if strings.Contains(beforeEnts[0].GetId(), "Admins") {
		t.Errorf("entitlement ID %q embeds the role's display name", beforeEnts[0].GetId())
	}
}

// TestChannelListSkipsUngovernedTypes checks that categories and threads are
// not synced as channels: categories hold no members, and threads inherit their
// parent channel's overwrites.
func TestChannelListSkipsUngovernedTypes(t *testing.T) {
	fake := newFakeDiscord(t)
	fake.handleJSON("GET", "/guilds/"+testGuildID+"/channels", []map[string]any{
		channelJSON(channelTextID, "general", discord.ChannelTypeGuildText, nil),
		channelJSON(channelVoiceID, "voice-room", discord.ChannelTypeGuildVoice, nil),
		channelJSON(channelCategoryID, "Rooms", discord.ChannelTypeGuildCategory, nil),
		channelJSON(channelThreadID, "a-thread", discord.ChannelTypeGuildPublicThread, nil),
	})

	builder := newChannelBuilder(fake.newClient(t))
	resources, _, err := builder.List(context.Background(), guildResourceID(t), resource_sdk.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	got := map[string]bool{}
	for _, res := range resources {
		got[res.GetId().GetResource()] = true
	}
	if !got[channelTextID] || !got[channelVoiceID] {
		t.Errorf("text and voice channels must be synced, got %v", got)
	}
	if got[channelCategoryID] {
		t.Error("category channels must not be synced")
	}
	if got[channelThreadID] {
		t.Error("threads must not be synced")
	}
}

// TestChannelEntitlementsMatchChannelType checks that voice channels do not
// advertise text permissions and vice versa. The previous implementation gave
// voice channels the union of both sets.
func TestChannelEntitlementsMatchChannelType(t *testing.T) {
	fake := newFakeDiscord(t)
	fake.handleJSON("GET", "/channels/"+channelTextID,
		channelJSON(channelTextID, "general", discord.ChannelTypeGuildText, nil))
	fake.handleJSON("GET", "/channels/"+channelVoiceID,
		channelJSON(channelVoiceID, "voice-room", discord.ChannelTypeGuildVoice, nil))

	builder := newChannelBuilder(fake.newClient(t))
	ctx := context.Background()
	parent := guildResourceID(t)

	slugsFor := func(channelID, name string) map[string]bool {
		ents, _, err := builder.Entitlements(ctx,
			resourceFor(t, channelResourceType, channelID, name, parent), resource_sdk.SyncOpAttrs{})
		if err != nil {
			t.Fatalf("Entitlements(%s): %v", name, err)
		}
		slugs := map[string]bool{}
		for _, ent := range ents {
			slugs[ent.GetSlug()] = true
		}
		return slugs
	}

	text := slugsFor(channelTextID, "general")
	voice := slugsFor(channelVoiceID, "voice-room")

	if !text[slugSendMessages] {
		t.Error("a text channel must expose send_messages")
	}
	// Since text-in-voice, messaging permissions are settable on a voice
	// channel's overwrite too, so they must be advertised there.
	if !voice[slugSendMessages] {
		t.Error("a voice channel must expose send_messages: voice channels carry a text chat")
	}
	if !voice["connect"] {
		t.Error("a voice channel must expose connect")
	}
	if text["connect"] {
		t.Error("a text channel must not expose the voice permission connect")
	}
	// Threads are the genuinely text-exclusive area.
	if !text["create_public_threads"] {
		t.Error("a text channel must expose create_public_threads")
	}
	if voice["create_public_threads"] {
		t.Error("a voice channel must not expose thread permissions: it hosts no threads")
	}
	// view_channel applies to every governed channel type.
	if !text["view_channel"] || !voice["view_channel"] {
		t.Error("view_channel must apply to both text and voice channels")
	}
}

// TestChannelGrantsComeFromOverwriteAllowBits is the regression test for the
// most consequential bug in the previous implementation: role grants were
// derived from the role's *server-wide* permission bitmask rather than the
// channel overwrite's allow mask, so it reported channel access the channel
// never conferred.
//
// It also covers a permission above bit 31 (SendMessagesInThreads is bit 38) to
// prove 64-bit bitmasks survive intact, and asserts that denied permissions are
// not reported as grants.
func TestChannelGrantsComeFromOverwriteAllowBits(t *testing.T) {
	fake := newFakeDiscord(t)

	fake.handleJSON("GET", "/channels/"+channelTextID, channelJSON(
		channelTextID, "general", discord.ChannelTypeGuildText, []map[string]any{
			// The @everyone role may view and post.
			overwriteJSON(roleEveryoneID, overwriteWireTypeRole,
				discord.PermissionViewChannel|discord.PermissionSendMessages, 0),
			// Alice additionally gets a permission above bit 31, and is
			// explicitly denied one that must not surface as a grant.
			overwriteJSON(userAliceID, overwriteWireTypeMember,
				discord.PermissionSendMessagesInThreads, discord.PermissionAttachFiles),
		}))

	builder := newChannelBuilder(fake.newClient(t))
	grants, _, err := builder.Grants(context.Background(),
		resourceFor(t, channelResourceType, channelTextID, "general", guildResourceID(t)),
		resource_sdk.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}

	type key struct{ principalType, principalID, slug string }
	got := map[key]bool{}
	for _, g := range grants {
		slug := g.GetEntitlement().GetId()
		if idx := strings.LastIndex(slug, ":"); idx >= 0 {
			slug = slug[idx+1:]
		}
		got[key{
			g.GetPrincipal().GetId().GetResourceType(),
			g.GetPrincipal().GetId().GetResource(),
			slug,
		}] = true
	}

	for _, want := range []key{
		{roleResourceTypeID, roleEveryoneID, "view_channel"},
		{roleResourceTypeID, roleEveryoneID, slugSendMessages},
		{userResourceTypeID, userAliceID, "send_messages_in_threads"},
	} {
		if !got[want] {
			t.Errorf("missing grant %+v", want)
		}
	}

	// The denied permission must not be reported.
	if got[key{userResourceTypeID, userAliceID, "attach_files"}] {
		t.Error("a denied permission must not be reported as a grant")
	}
	// @everyone was not granted anything beyond view and post.
	if got[key{roleResourceTypeID, roleEveryoneID, "manage_messages"}] {
		t.Error("reported a grant the overwrite's allow mask does not contain")
	}
}

// TestChannelGrantIsReadModifyWrite checks the overwrite PUT: Discord replaces
// the whole overwrite, so the existing allow bits must be preserved, the new
// bit set, and the same bit cleared from deny (an explicit deny outranks an
// allow and would make the grant inert).
func TestChannelGrantIsReadModifyWrite(t *testing.T) {
	fake := newFakeDiscord(t)

	fake.handleJSON("GET", "/channels/"+channelTextID, channelJSON(
		channelTextID, "general", discord.ChannelTypeGuildText, []map[string]any{
			overwriteJSON(userAliceID, overwriteWireTypeMember,
				discord.PermissionViewChannel, discord.PermissionSendMessages),
		}))
	fake.handle("PUT", "/channels/"+channelTextID+"/permissions/"+userAliceID,
		func(w http.ResponseWriter, _ *http.Request, _ recordedRequest) {
			w.WriteHeader(http.StatusNoContent)
		})

	builder := newChannelBuilder(fake.newClient(t))
	parent := guildResourceID(t)
	channel := resourceFor(t, channelResourceType, channelTextID, "general", parent)

	ent := &v2.Entitlement{
		Id:       fmt.Sprintf("%s:%s:%s", channelResourceTypeID, channelTextID, slugSendMessages),
		Slug:     slugSendMessages,
		Resource: channel,
	}
	principal := resourceFor(t, userResourceType, userAliceID, "Alice", parent)

	grants, _, err := builder.Grant(context.Background(), principal, ent)
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if len(grants) != 1 {
		t.Errorf("Grant returned %d grants, want 1 so C1 records access without waiting for a sync", len(grants))
	}

	requests := fake.requestsFor("PUT", "/channels/"+channelTextID+"/permissions/"+userAliceID)
	if len(requests) != 1 {
		t.Fatalf("made %d overwrite writes, want 1", len(requests))
	}

	var body struct {
		ID    string `json:"id"`
		Type  int    `json:"type"`
		Allow string `json:"allow"`
		Deny  string `json:"deny"`
	}
	if err := json.Unmarshal([]byte(requests[0].Body), &body); err != nil {
		t.Fatalf("decoding overwrite body %q: %v", requests[0].Body, err)
	}

	wantAllow := fmt.Sprintf("%d", discord.PermissionViewChannel|discord.PermissionSendMessages)
	if body.Allow != wantAllow {
		t.Errorf("allow = %q, want %q (existing bits preserved plus the granted bit)", body.Allow, wantAllow)
	}
	if body.Deny != "0" {
		t.Errorf("deny = %q, want 0 (the granted bit must be cleared from deny)", body.Deny)
	}
	if body.Type != int(overwriteWireTypeMember) {
		t.Errorf("overwrite type = %d, want %d", body.Type, overwriteWireTypeMember)
	}
}

// TestChannelRevokeDeletesEmptiedOverwrite checks that clearing the last bit
// removes the overwrite rather than leaving an empty one behind.
func TestChannelRevokeDeletesEmptiedOverwrite(t *testing.T) {
	fake := newFakeDiscord(t)

	fake.handleJSON("GET", "/channels/"+channelTextID, channelJSON(
		channelTextID, "general", discord.ChannelTypeGuildText, []map[string]any{
			overwriteJSON(userAliceID, overwriteWireTypeMember,
				discord.PermissionSendMessages, 0),
		}))
	fake.handle("DELETE", "/channels/"+channelTextID+"/permissions/"+userAliceID,
		func(w http.ResponseWriter, _ *http.Request, _ recordedRequest) {
			w.WriteHeader(http.StatusNoContent)
		})

	builder := newChannelBuilder(fake.newClient(t))
	parent := guildResourceID(t)
	channel := resourceFor(t, channelResourceType, channelTextID, "general", parent)

	_, err := builder.Revoke(context.Background(), &v2.Grant{
		Entitlement: &v2.Entitlement{
			Id:       fmt.Sprintf("%s:%s:%s", channelResourceTypeID, channelTextID, slugSendMessages),
			Slug:     slugSendMessages,
			Resource: channel,
		},
		Principal: resourceFor(t, userResourceType, userAliceID, "Alice", parent),
	})
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	if len(fake.requestsFor("DELETE", "/channels/"+channelTextID+"/permissions/"+userAliceID)) != 1 {
		t.Error("an overwrite left with no allow and no deny bits must be deleted")
	}
	if len(fake.requestsFor("PUT", "/channels/"+channelTextID+"/permissions/"+userAliceID)) != 0 {
		t.Error("an emptied overwrite must not be rewritten")
	}
}

// TestChannelRevokeWithoutOverwriteIsAlreadyRevoked covers idempotency: a
// principal with no overwrite is already in the desired state, which is a
// success with an annotation rather than an error.
func TestChannelRevokeWithoutOverwriteIsAlreadyRevoked(t *testing.T) {
	fake := newFakeDiscord(t)
	fake.handleJSON("GET", "/channels/"+channelTextID,
		channelJSON(channelTextID, "general", discord.ChannelTypeGuildText, []map[string]any{}))

	builder := newChannelBuilder(fake.newClient(t))
	parent := guildResourceID(t)

	annos, err := builder.Revoke(context.Background(), &v2.Grant{
		Entitlement: &v2.Entitlement{
			Id:       fmt.Sprintf("%s:%s:%s", channelResourceTypeID, channelTextID, slugSendMessages),
			Slug:     slugSendMessages,
			Resource: resourceFor(t, channelResourceType, channelTextID, "general", parent),
		},
		Principal: resourceFor(t, userResourceType, userStrangerID, "Stranger", parent),
	})
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if !annos.Contains((*v2.GrantAlreadyRevoked)(nil)) {
		t.Error("revoking a permission that was never allowed must report GrantAlreadyRevoked")
	}
}

// TestRoleGrantAndRevoke covers the role provisioning request shapes and that
// the returned grant matches what a sync would emit for the same access.
func TestRoleGrantAndRevoke(t *testing.T) {
	fake := newFakeDiscord(t)
	rolePath := "/guilds/" + testGuildID + "/members/" + userAliceID + "/roles/" + roleAdminsID
	for _, method := range []string{"PUT", "DELETE"} {
		fake.handle(method, rolePath, func(w http.ResponseWriter, _ *http.Request, _ recordedRequest) {
			w.WriteHeader(http.StatusNoContent)
		})
	}

	builder := newRoleBuilder(fake.newClient(t))
	ctx := context.Background()
	parent := guildResourceID(t)
	role := resourceFor(t, roleResourceType, roleAdminsID, "Admins", parent)
	principal := resourceFor(t, userResourceType, userAliceID, "Alice", parent)

	ent := &v2.Entitlement{
		Id:       fmt.Sprintf("%s:%s:%s", roleResourceTypeID, roleAdminsID, roleMemberEntitlement),
		Slug:     roleMemberEntitlement,
		Resource: role,
	}

	grants, _, err := builder.Grant(ctx, principal, ent)
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if len(grants) != 1 {
		t.Fatalf("Grant returned %d grants, want 1", len(grants))
	}
	if len(fake.requestsFor("PUT", rolePath)) != 1 {
		t.Error("Grant must PUT the role onto the member")
	}

	// The grant returned by provisioning has to carry the same ID the sync path
	// would produce, or C1 sees two different grants for one assignment.
	fake.handleJSON("GET", "/guilds/"+testGuildID+"/members", []map[string]any{
		memberJSON(userAliceID, "alice", "", "Alice", false, []string{roleAdminsID}),
	})
	synced, _, err := builder.Grants(ctx, role, resource_sdk.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if len(synced) != 1 {
		t.Fatalf("sync produced %d grants, want 1", len(synced))
	}
	if grants[0].GetId() != synced[0].GetId() {
		t.Errorf("provisioned grant ID %q does not match synced grant ID %q",
			grants[0].GetId(), synced[0].GetId())
	}

	if _, err := builder.Revoke(ctx, synced[0]); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if len(fake.requestsFor("DELETE", rolePath)) != 1 {
		t.Error("Revoke must DELETE the role from the member")
	}
}

// TestRoleProvisioningRejectsEveryone checks that the implicit @everyone role
// is refused rather than sent to Discord, which would answer with a confusing
// permissions error.
func TestRoleProvisioningRejectsEveryone(t *testing.T) {
	fake := newFakeDiscord(t)
	builder := newRoleBuilder(fake.newClient(t))
	parent := guildResourceID(t)

	ent := &v2.Entitlement{
		Id:       fmt.Sprintf("%s:%s:%s", roleResourceTypeID, roleEveryoneID, roleMemberEntitlement),
		Slug:     roleMemberEntitlement,
		Resource: resourceFor(t, roleResourceType, roleEveryoneID, "@everyone", parent),
	}

	_, _, err := builder.Grant(context.Background(),
		resourceFor(t, userResourceType, userAliceID, "Alice", parent), ent)
	if err == nil {
		t.Error("granting @everyone must be refused")
	}
	if len(fake.requests) != 0 {
		t.Errorf("granting @everyone issued %d upstream requests, want 0", len(fake.requests))
	}
}

// TestRoleRevokeTreatsMissingMemberAsRevoked covers idempotency for a member
// who has already left the server.
func TestRoleRevokeTreatsMissingMemberAsRevoked(t *testing.T) {
	fake := newFakeDiscord(t)
	rolePath := "/guilds/" + testGuildID + "/members/" + userAliceID + "/roles/" + roleAdminsID
	fake.handleStatus("DELETE", rolePath, http.StatusNotFound, int(rest.JSONErrorCodeUnknownMember))

	builder := newRoleBuilder(fake.newClient(t))
	parent := guildResourceID(t)

	annos, err := builder.Revoke(context.Background(), &v2.Grant{
		Entitlement: &v2.Entitlement{
			Id:       fmt.Sprintf("%s:%s:%s", roleResourceTypeID, roleAdminsID, roleMemberEntitlement),
			Slug:     roleMemberEntitlement,
			Resource: resourceFor(t, roleResourceType, roleAdminsID, "Admins", parent),
		},
		Principal: resourceFor(t, userResourceType, userAliceID, "Alice", parent),
	})
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if !annos.Contains((*v2.GrantAlreadyRevoked)(nil)) {
		t.Error("revoking a role from an absent member must report GrantAlreadyRevoked")
	}
}

// TestGuildRevokeTreatsMissingMemberAsRevoked covers the same idempotency rule
// for server membership.
func TestGuildRevokeTreatsMissingMemberAsRevoked(t *testing.T) {
	fake := newFakeDiscord(t)
	fake.handleStatus("DELETE", "/guilds/"+testGuildID+"/members/"+userAliceID,
		http.StatusNotFound, int(rest.JSONErrorCodeUnknownMember))

	builder := newGuildBuilder(fake.newClient(t))

	annos, err := builder.Revoke(context.Background(), &v2.Grant{
		Entitlement: &v2.Entitlement{
			Id:       fmt.Sprintf("%s:%s:%s", guildResourceTypeID, testGuildID, guildAccessEntitlement),
			Slug:     guildAccessEntitlement,
			Resource: resourceFor(t, guildResourceType, testGuildID, testGuildName, nil),
		},
		Principal: resourceFor(t, userResourceType, userAliceID, "Alice", nil),
	})
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if !annos.Contains((*v2.GrantAlreadyRevoked)(nil)) {
		t.Error("removing an absent member must report GrantAlreadyRevoked")
	}
}

// TestGuildGrantInvitesViaRulesChannel covers the invite flow: the server's
// nominated rules channel is preferred over any other text channel, and the
// invite is DM'd to the principal.
func TestGuildGrantInvitesViaRulesChannel(t *testing.T) {
	fake := newFakeDiscord(t)
	const dmChannelID = "700000000000000900"

	fake.handleJSON("GET", "/guilds/"+testGuildID, map[string]any{
		"id":               testGuildID,
		keyName:            testGuildName,
		"rules_channel_id": channelTextID,
	})
	fake.handleJSON("GET", "/guilds/"+testGuildID+"/channels", []map[string]any{
		// Deliberately ordered so the rules channel is not the fallback pick.
		channelJSON("700000000000000199", "zzz-general", discord.ChannelTypeGuildText, nil),
		channelJSON(channelTextID, "rules", discord.ChannelTypeGuildText, nil),
	})
	fake.handleJSON("POST", "/channels/"+channelTextID+"/invites", map[string]any{keyCode: "abc123"})
	fake.handleJSON("POST", "/users/@me/channels", map[string]any{"id": dmChannelID})
	fake.handleJSON("POST", "/channels/"+dmChannelID+"/messages", map[string]any{"id": "1"})

	builder := newGuildBuilder(fake.newClient(t))
	guild := resourceFor(t, guildResourceType, testGuildID, testGuildName, nil)

	grants, _, err := builder.Grant(context.Background(),
		resourceFor(t, userResourceType, userAliceID, "Alice", nil),
		&v2.Entitlement{
			Id:       fmt.Sprintf("%s:%s:%s", guildResourceTypeID, testGuildID, guildAccessEntitlement),
			Slug:     guildAccessEntitlement,
			Resource: guild,
		})
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	// The user has not joined yet, so there is no grant to report.
	if len(grants) != 0 {
		t.Errorf("Grant returned %d grants, want 0 until the invite is accepted", len(grants))
	}

	if len(fake.requestsFor("POST", "/channels/"+channelTextID+"/invites")) != 1 {
		t.Error("the invite must be created in the server's rules channel")
	}
	messages := fake.requestsFor("POST", "/channels/"+dmChannelID+"/messages")
	if len(messages) != 1 {
		t.Fatal("the invite must be sent as a direct message")
	}
	if !strings.Contains(messages[0].Body, "abc123") {
		t.Errorf("the DM must carry the invite code, got %q", messages[0].Body)
	}
}

// TestProvisioningRejectsUnknownEntitlement checks that a slug this connector
// does not model is refused instead of being acted on with a wrong assumption.
func TestProvisioningRejectsUnknownEntitlement(t *testing.T) {
	fake := newFakeDiscord(t)
	builder := newRoleBuilder(fake.newClient(t))
	parent := guildResourceID(t)

	_, _, err := builder.Grant(context.Background(),
		resourceFor(t, userResourceType, userAliceID, "Alice", parent),
		&v2.Entitlement{
			Id:       fmt.Sprintf("%s:%s:%s", roleResourceTypeID, roleAdminsID, "owner"),
			Resource: resourceFor(t, roleResourceType, roleAdminsID, "Admins", parent),
		})
	if err == nil {
		t.Error("an unknown entitlement slug must be refused")
	}
	if len(fake.requests) != 0 {
		t.Errorf("an unknown entitlement issued %d upstream requests, want 0", len(fake.requests))
	}
}

// TestEntitlementSlugFallsBackToID covers recovering a slug from the
// entitlement ID, which is the path taken when C1 sends an entitlement whose
// Slug field is not populated.
func TestEntitlementSlugFallsBackToID(t *testing.T) {
	for _, tc := range []struct {
		name string
		ent  *v2.Entitlement
		want string
	}{
		{"slug field wins", &v2.Entitlement{Slug: roleMemberEntitlement, Id: "role:1:ignored"}, roleMemberEntitlement},
		{"from id", &v2.Entitlement{Id: "role:1:" + roleMemberEntitlement}, roleMemberEntitlement},
		{"slug containing a colon", &v2.Entitlement{Id: "channel:1:a:b"}, "a:b"},
		{"malformed id", &v2.Entitlement{Id: "role:1"}, ""},
		{"nil", nil, ""},
	} {
		if got := entitlementSlug(tc.ent); got != tc.want {
			t.Errorf("%s: entitlementSlug = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestUseEmbeddedActivitiesIsVoiceScoped is the regression test for a
// mis-scoped permission. Discord classifies USE_EMBEDDED_ACTIVITIES (bit 39) as
// a voice permission — activities launch inside a voice channel — so scoping it
// to text both advertised it where it is meaningless and, worse, meant an
// explicit allow on a voice channel was never reported as a grant.
func TestUseEmbeddedActivitiesIsVoiceScoped(t *testing.T) {
	permission, ok := channelPermissionsBySlug["use_embedded_activities"]
	if !ok {
		t.Fatal("use_embedded_activities is not a known channel permission")
	}
	if permission.Scope != scopeVoice {
		t.Errorf("use_embedded_activities scope = %v, want scopeVoice", permission.Scope)
	}

	if !permissionAppliesTo(permission, discord.ChannelTypeGuildVoice) {
		t.Error("use_embedded_activities must apply to voice channels")
	}
	if permissionAppliesTo(permission, discord.ChannelTypeGuildText) {
		t.Error("use_embedded_activities must not apply to text channels")
	}
}

// TestVoiceChannelGrantsReportActivities checks the practical consequence of
// the scoping fix: an explicit allow of a voice-only permission on a voice
// channel is reported as a grant.
func TestVoiceChannelGrantsReportActivities(t *testing.T) {
	fake := newFakeDiscord(t)
	fake.handleJSON("GET", "/channels/"+channelVoiceID, channelJSON(
		channelVoiceID, "voice-room", discord.ChannelTypeGuildVoice, []map[string]any{
			overwriteJSON(userAliceID, overwriteWireTypeMember,
				discord.PermissionUseEmbeddedActivities, 0),
		}))

	builder := newChannelBuilder(fake.newClient(t))
	grants, _, err := builder.Grants(context.Background(),
		resourceFor(t, channelResourceType, channelVoiceID, "voice-room", guildResourceID(t)),
		resource_sdk.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}

	found := false
	for _, g := range grants {
		if strings.HasSuffix(g.GetEntitlement().GetId(), ":use_embedded_activities") {
			found = true
		}
	}
	if !found {
		t.Error("an explicit Use Activities allow on a voice channel must be reported as a grant")
	}
}

// TestMembersPageRejectsMemberWithoutUser covers two problems in one place.
//
// The original bug was a panic: the pagination cursor read Member.User.ID on
// the last member of a full page, while newMemberResource treated a nil
// Member.User as a real condition. Guarding only the cursor then introduced a
// subtler failure — roleBuilder.Grants filters by role before building a
// resource, so a full page ending in such a member would skip the guarded path
// entirely, end pagination, and silently drop every remaining member from that
// role's grants.
//
// Rejecting the page in the client keeps the failure attributable for every
// caller, which is what this test pins.
func TestMembersPageRejectsMemberWithoutUser(t *testing.T) {
	fake := newFakeDiscord(t)

	page := make([]map[string]any, 0, client.MemberPageSize)
	for i := 0; i < client.MemberPageSize-1; i++ {
		page = append(page,
			memberJSON(fmt.Sprintf("9000000000000%05d", i), fmt.Sprintf("member%d", i), "", "", false, nil))
	}
	// A full page whose final entry has no user object and holds no roles, so
	// a role-grant filter would skip it.
	page = append(page, map[string]any{keyRoles: []string{}, keyJoinedAt: testJoinedAt})
	fake.handleJSON("GET", "/guilds/"+testGuildID+"/members", page)

	discordClient := fake.newClient(t)
	ctx := context.Background()
	parent := guildResourceID(t)

	// The client must not panic, and must not report a usable page.
	if _, _, err := discordClient.MembersPage(ctx, testGuildID, ""); err == nil {
		t.Error("MembersPage must reject a page containing a member with no user object")
	}

	namesIntent := func(t *testing.T, caller string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: expected an error naming the missing intent", caller)
			return
		}
		if !strings.Contains(err.Error(), "Server Members Intent") {
			t.Errorf("%s: error should name the missing intent, got: %v", caller, err)
		}
	}

	_, _, err := newUserBuilder(discordClient).List(ctx, parent, resource_sdk.SyncOpAttrs{})
	namesIntent(t, "user List", err)

	_, _, err = newGuildBuilder(discordClient).Grants(ctx,
		resourceFor(t, guildResourceType, testGuildID, testGuildName, nil), resource_sdk.SyncOpAttrs{})
	namesIntent(t, "guild Grants", err)

	// The case a caller-side guard would have missed.
	_, _, err = newRoleBuilder(discordClient).Grants(ctx,
		resourceFor(t, roleResourceType, roleAdminsID, "Admins", parent), resource_sdk.SyncOpAttrs{})
	namesIntent(t, "role Grants", err)
}

// TestChannelGrantRejectsMismatchedPermission checks that a permission which
// does not apply to the channel's type is refused rather than written into the
// overwrite as a meaningless bit. Threads are the mismatch case because voice
// channels host no threads, whereas messaging permissions are legitimate there.
func TestChannelGrantRejectsMismatchedPermission(t *testing.T) {
	fake := newFakeDiscord(t)
	fake.handleJSON("GET", "/channels/"+channelVoiceID,
		channelJSON(channelVoiceID, "voice-room", discord.ChannelTypeGuildVoice, []map[string]any{}))

	builder := newChannelBuilder(fake.newClient(t))
	parent := guildResourceID(t)

	_, _, err := builder.Grant(context.Background(),
		resourceFor(t, userResourceType, userAliceID, "Alice", parent),
		&v2.Entitlement{
			Id:       fmt.Sprintf("%s:%s:%s", channelResourceTypeID, channelVoiceID, "create_public_threads"),
			Slug:     "create_public_threads",
			Resource: resourceFor(t, channelResourceType, channelVoiceID, "voice-room", parent),
		})
	if err == nil {
		t.Error("granting a thread permission on a voice channel must be refused")
	}
	if len(fake.requestsFor("PUT", "/channels/"+channelVoiceID+"/permissions/"+userAliceID)) != 0 {
		t.Error("a mismatched permission must not be written to the overwrite")
	}
}

// TestGuildGrantRevokesUndeliverableInvite covers the invite-leak fix: when the
// DM cannot be delivered, the invite is revoked rather than left redeemable,
// and its code is kept out of the error, which reaches logs and C1 task output.
func TestGuildGrantRevokesUndeliverableInvite(t *testing.T) {
	fake := newFakeDiscord(t)
	const dmChannelID = "700000000000000900"
	const inviteCode = "supersecretcode"

	fake.handleJSON("GET", "/guilds/"+testGuildID, map[string]any{
		"id":               testGuildID,
		keyName:            testGuildName,
		"rules_channel_id": channelTextID,
	})
	fake.handleJSON("GET", "/guilds/"+testGuildID+"/channels", []map[string]any{
		channelJSON(channelTextID, "rules", discord.ChannelTypeGuildText, nil),
	})
	fake.handleJSON("POST", "/channels/"+channelTextID+"/invites", map[string]any{keyCode: inviteCode})
	fake.handleJSON("POST", "/users/@me/channels", map[string]any{"id": dmChannelID})
	// Discord refuses to deliver the DM.
	fake.handleStatus("POST", "/channels/"+dmChannelID+"/messages",
		http.StatusForbidden, int(rest.JSONErrorCodeCannotSendMessagesToThisUser))
	fake.handleJSON("DELETE", "/invites/"+inviteCode, map[string]any{keyCode: inviteCode})

	builder := newGuildBuilder(fake.newClient(t))
	_, _, err := builder.Grant(context.Background(),
		resourceFor(t, userResourceType, userAliceID, "Alice", nil),
		&v2.Entitlement{
			Id:       fmt.Sprintf("%s:%s:%s", guildResourceTypeID, testGuildID, guildAccessEntitlement),
			Slug:     guildAccessEntitlement,
			Resource: resourceFor(t, guildResourceType, testGuildID, testGuildName, nil),
		})
	if err == nil {
		t.Fatal("an undeliverable invitation must not report success")
	}
	if strings.Contains(err.Error(), inviteCode) {
		t.Errorf("the error must not leak the redeemable invite code, got: %v", err)
	}
	if len(fake.requestsFor("DELETE", "/invites/"+inviteCode)) != 1 {
		t.Error("an undeliverable invite must be revoked so it is not left redeemable")
	}
}

// TestGuildIDForProvisioningFallsBackToPrincipal covers resolving the server
// for a provisioning request that did not round-trip the entitlement resource's
// parent.
func TestGuildIDForProvisioningFallsBackToPrincipal(t *testing.T) {
	parent := guildResourceID(t)
	roleWithParent := resourceFor(t, roleResourceType, roleAdminsID, "Admins", parent)
	roleWithoutParent := resourceFor(t, roleResourceType, roleAdminsID, "Admins", nil)
	principalWithParent := resourceFor(t, userResourceType, userAliceID, "Alice", parent)
	principalWithoutParent := resourceFor(t, userResourceType, userAliceID, "Alice", nil)

	// The entitlement resource wins when it has a parent: a role belongs to
	// exactly one server, while an account belongs to many.
	otherGuild, err := resource_sdk.NewResourceID(guildResourceType, "700000000000000999")
	if err != nil {
		t.Fatalf("NewResourceID: %v", err)
	}
	got, err := guildIDForProvisioning(roleWithParent,
		resourceFor(t, userResourceType, userAliceID, "Alice", otherGuild))
	if err != nil {
		t.Fatalf("guildIDForProvisioning: %v", err)
	}
	if got != testGuildID {
		t.Errorf("guild = %q, want the role's parent %q", got, testGuildID)
	}

	// Fall back to the principal only when the role carries no parent.
	got, err = guildIDForProvisioning(roleWithoutParent, principalWithParent)
	if err != nil {
		t.Fatalf("guildIDForProvisioning fallback: %v", err)
	}
	if got != testGuildID {
		t.Errorf("fallback guild = %q, want %q", got, testGuildID)
	}

	// With neither source available, fail rather than guess.
	if _, err := guildIDForProvisioning(roleWithoutParent, principalWithoutParent); err == nil {
		t.Error("expected an error when no parent server can be resolved")
	}
}
