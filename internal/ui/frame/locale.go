package frame

import "context"

func returnPath(ctx context.Context) string {
	if p := ReturnPath(ctx); p != "" {
		return p
	}
	return "/"
}
