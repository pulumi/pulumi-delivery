//go:build nodejs || all

package examples

import "testing"

// The TypeScript program must typecheck against the built Node SDK (sdk/nodejs/bin).
func TestNodejsTypecheck(t *testing.T) {
	// Not npm ci: the lockfile records the SDK version, and CI builds the SDK with a different one.
	// --install-links copies the SDK instead of linking it, so its imports resolve from this program.
	run(t, "basic", "npm", "install", "--install-links")
	run(t, "basic", "npx", "tsc", "--noEmit", "--skipLibCheck", "--target", "es2020", "--module", "commonjs",
		"--moduleResolution", "node", "--strict", "index.ts")
}

func TestNodejsPreview(t *testing.T) {
	run(t, "basic", "npm", "install", "--install-links")
	preview(t, "basic")
}
