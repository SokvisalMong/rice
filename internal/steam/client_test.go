package steam

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestSearchFiltersNonAppsAndPreservesOrder(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/storesearch/" {
			t.Errorf("path = %q, want /api/storesearch/", request.URL.Path)
		}
		if got := request.URL.Query().Get("term"); got != "Portal" {
			t.Errorf("term = %q, want Portal", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{
			"items": [
				{"type":"bundle","name":"Portal Bundle","id":10},
				{"type":"app","name":"Portal","id":400,"tiny_image":"portal.jpg"},
				{"type":"app","name":"Portal 2","id":620,"tiny_image":"portal2.jpg"}
			]
		}`)
	}))
	defer server.Close()

	client := steamClientForServer(t, server)
	games, err := client.Search(context.Background(), " Portal ", 2)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(games) != 2 {
		t.Fatalf("Search() returned %d games, want 2", len(games))
	}
	if games[0] != (Game{ID: 400, Name: "Portal", ImageURL: "portal.jpg"}) {
		t.Errorf("first game = %#v", games[0])
	}
	if games[1].ID != 620 {
		t.Errorf("second game ID = %d, want 620", games[1].ID)
	}
}

func steamClientForServer(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	baseURL, err := url.Parse(server.URL + "/api/")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(server.Client())
	client.baseURL = baseURL
	return client
}
