package prometheus

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestQuery(t *testing.T) {
	c, err := New("http://prometheus:9090", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v1/query" || r.URL.Query().Get("query") != "up" {
			t.Fatalf("bad request: %s", r.URL.String())
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(bytes.NewBufferString(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"instance":"db:10080"},"value":[1,"2.5"]}]}}`))}, nil
	})}
	got, err := c.Query(context.Background(), "up")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Value != 2.5 || got[0].Metric["instance"] != "db:10080" {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestNewRejectsInvalidURL(t *testing.T) {
	if _, err := New("localhost:9090", time.Second); err == nil {
		t.Fatal("expected invalid URL error")
	}
}
