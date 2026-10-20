package billing

import (
	"context"
	"testing"
)

func TestAccept03ChargeRetriesA503(t *testing.T) {
	gw, posts := fakeGateway(t, reply{503, ""}, reply{200, `{"id":"r2","amount":500}`})
	if _, err := Charge(context.Background(), Order{ID: 2, Total: 500}, gw); err != nil {
		t.Errorf("Charge: %v", err)
	}
	if posts.Load() != 2 {
		t.Errorf("posts = %d, want 2", posts.Load())
	}
}

func TestAccept03ChargeGivesUpAfterThreePosts(t *testing.T) {
	gw, posts := fakeGateway(t, reply{503, ""})
	if _, err := Charge(context.Background(), Order{ID: 3, Total: 500}, gw); err == nil {
		t.Error("Charge succeeded, want an error")
	}
	if posts.Load() != 3 {
		t.Errorf("posts = %d, want 3", posts.Load())
	}
}
