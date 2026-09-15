package naming

import "testing"

// A Drupal machine name, and nothing that could escape a path or a project
// name — every caller turns one of these into a path segment.
func TestWhatCountsAsAModuleName(t *testing.T) {
	for _, name := range []string{"pathauto", "token", "field_visibility_conditions", "a", "d8_module"} {
		if !IsModuleName(name) {
			t.Errorf("%q is a machine name and was refused", name)
		}
	}

	refused := []string{
		"",           // nothing is not a name
		"Pathauto",   // machine names are lower case
		"path-auto",  // hyphens are the *project* name convention
		"8pathauto",  // must start with a letter
		"path auto",  // a space would split an argument vector
		"../escape",  // the reason this rule exists at all
		"path/auto",  // a second path segment
		"pathauto\n", // a trailing newline from a careless read
		"path.auto",  // a dotfile, or the current directory
		"pathauto ",  // trailing space
		"_pathauto",  // must start with a letter
		"páthauto",   // ASCII only
	}
	for _, name := range refused {
		if IsModuleName(name) {
			t.Errorf("%q was accepted as a machine name", name)
		}
	}
}

// A whole major version number and nothing else: "11.2" is a core version, not
// a major, and the two are never interchangeable.
func TestWhatCountsAsACoreMajor(t *testing.T) {
	for _, major := range []string{"8", "10", "11", "12", "123"} {
		if !IsCoreMajor(major) {
			t.Errorf("%q is a major and was refused", major)
		}
	}

	for _, major := range []string{"", "11.2", "11.x", "v11", "-11", "11 ", " 11", "11\n", "1e1"} {
		if IsCoreMajor(major) {
			t.Errorf("%q was accepted as a major", major)
		}
	}
}
