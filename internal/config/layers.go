package config

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// The layers a setting can come from, lowest precedence first. A later layer wins the keys it
// names and leaves the rest alone, which is what makes absence of configuration cost nothing.
const (
	LayerDefault    = "default"
	LayerProfile    = "profile"
	LayerUser       = "user"
	LayerProject    = "project"
	LayerInvocation = "invocation"
)

// ProfileSubdir is where every layer keeps its named round policies.
const ProfileSubdir = "profiles"

// defaultsRoot is the embedded layer's directory inside this package.
const defaultsRoot = "defaults"

//go:embed defaults
var builtin embed.FS

// Sources are the directories the file layers are read from, plus whatever the invocation set.
// An empty directory is a layer that is not present, which is not an error: the built-in defaults
// alone are a complete configuration.
type Sources struct {
	// User is the herdr plugin config directory.
	User string
	// Project is <repo>/.review-loop, the directory a project commits.
	Project string
	// Invocation holds the settings named on the command line, keyed like config.json.
	Invocation map[string]any
}

// Profile records which round policy was chosen and where it was read from, so a month-old run
// can be explained from its manifest rather than from today's files.
type Profile struct {
	Name, Layer, Path, Description string
}

// Resolution is a fully resolved configuration together with the record of where each part of it
// came from. Everything the `config` subcommand prints is in here; nothing has to be re-derived.
type Resolution struct {
	Values   Values
	Profile  Profile
	Layers   map[string]string
	Warnings []string
}

// Resolve merges the layers into one configuration. It cannot fail: an unreadable or malformed
// file becomes a warning and the layers below it stand, because `stop` has to work when a
// config.json is broken.
//
// The profile sits directly above the built-in defaults and below the user's own config.json. A
// profile is a named set of defaults for a kind of review, so a setting somebody wrote by hand
// outranks one a policy file happens to restate. §6.3 fixes the order of the four file layers and
// leaves the profile's place in it open; this is that choice, and the `config` subcommand shows
// it, so a surprising result is one command away from being explained.
func Resolve(sources Sources) Resolution {
	files := fileLayers(sources)
	// two passes: the profile name is itself a setting, so which profile applies is only known
	// once the file layers have been merged, and the profile then merges in beneath them.
	name := Defaults().Profile
	var warnings []string
	for _, document := range files {
		if chosen, ok := document.keys["profile"].(string); ok && strings.TrimSpace(chosen) != "" {
			name = strings.TrimSpace(chosen)
		}
		warnings = append(warnings, document.warnings...)
	}
	profileDocument, profile, profileWarnings := loadProfile(sources, name)
	warnings = append(warnings, profileWarnings...)

	values := Defaults()
	layers := map[string]string{}
	for _, field := range Fields() {
		layers[field.Key] = LayerDefault
	}
	for _, key := range structured {
		layers[key] = LayerDefault
	}
	documents := append([]document{profileDocument}, files...)
	for _, document := range documents {
		warnings = append(warnings, document.applyTo(&values, layers)...)
	}
	return Resolution{Values: values, Profile: profile, Layers: layers, Warnings: dedupe(warnings)}
}

// fileLayers reads the config.json of every file layer, lowest first, plus the invocation layer.
func fileLayers(sources Sources) []document {
	var documents []document
	for _, layer := range []struct{ name, dir string }{{LayerUser, sources.User}, {LayerProject, sources.Project}} {
		if layer.dir == "" {
			continue
		}
		found, ok := readFile(Path(layer.dir), layer.name)
		if ok || len(found.warnings) != 0 {
			documents = append(documents, found)
		}
	}
	if len(sources.Invocation) != 0 {
		documents = append(documents, document{layer: LayerInvocation, keys: flatten(sources.Invocation)})
	}
	return documents
}

// loadProfile finds the named policy in the highest layer that has a file for it. Resolution is
// per file, not per key: two profiles of the same name in different layers are two policies, and
// half of each is not a policy. A candidate that could not be read is skipped but never silently:
// its warning travels with the profile that ended up being used, or the user is told nothing about
// the policy file they wrote and the run quietly obeys a different one.
func loadProfile(sources Sources, name string) (document, Profile, []string) {
	candidates := []struct{ layer, path string }{
		{LayerProject, profilePath(sources.Project, name)},
		{LayerUser, profilePath(sources.User, name)},
	}
	var warnings []string
	for _, candidate := range candidates {
		if candidate.path == "" {
			continue
		}
		found, ok := readFile(candidate.path, LayerProfile)
		if !ok {
			warnings = append(warnings, found.warnings...)
			continue
		}
		profile := Profile{Name: name, Layer: candidate.layer, Path: candidate.path, Description: found.description()}
		selected := found.asProfile()
		return selected, profile, append(warnings, selected.warnings...)
	}
	embedded := defaultsRoot + "/" + ProfileSubdir + "/" + name + ".json"
	found, ok := readDocument(builtin, embedded, LayerProfile, "built-in profile "+name)
	if !ok {
		missing := fmt.Sprintf("profile %q was not found in any layer, using the built-in rounds", name)
		return document{layer: LayerProfile}, Profile{Name: name}, append(append(warnings, found.warnings...), missing)
	}
	profile := Profile{Name: name, Layer: LayerDefault, Path: embedded, Description: found.description()}
	selected := found.asProfile()
	return selected, profile, append(warnings, selected.warnings...)
}

// Profiles lists every round policy the layers make available, each named once with the layer
// that would win it. It is what `config` prints and what tells a user which names are selectable.
func Profiles(sources Sources) []Profile {
	winner := map[string]Profile{}
	// lowest layer first, so a higher one overwrites the entry rather than adding a second
	for _, layer := range []struct{ name, dir string }{{LayerDefault, ""}, {LayerUser, sources.User}, {LayerProject, sources.Project}} {
		for _, name := range profileNames(layer.dir) {
			_, profile, _ := loadProfile(profileSources(sources, layer.name), name)
			winner[name] = profile
		}
	}
	names := make([]string, 0, len(winner))
	for name := range winner {
		names = append(names, name)
	}
	sort.Strings(names)
	profiles := make([]Profile, 0, len(names))
	for _, name := range names {
		profiles = append(profiles, winner[name])
	}
	return profiles
}

// profileSources narrows the layers to those at or below the one a name was found in, so a
// profile is reported against the layer that actually supplies it.
func profileSources(sources Sources, layer string) Sources {
	switch layer {
	case LayerProject:
		return sources
	case LayerUser:
		return Sources{User: sources.User}
	default:
		return Sources{}
	}
}

// profileNames lists the profile files in one layer directory; an empty directory names the
// built-in layer, whose profiles are compiled in.
func profileNames(dir string) []string {
	fsys := profilesFS(dir)
	if fsys == nil {
		return nil
	}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if name, ok := strings.CutSuffix(entry.Name(), ".json"); ok && !entry.IsDir() {
			names = append(names, name)
		}
	}
	return names
}

func profilesFS(dir string) fs.FS {
	if dir == "" {
		embedded, err := fs.Sub(builtin, defaultsRoot+"/"+ProfileSubdir)
		if err != nil {
			return nil
		}
		return embedded
	}
	return os.DirFS(filepath.Join(dir, ProfileSubdir))
}

// ProfileFile returns the winning profile's bytes and where they came from, which is what `init`
// copies down into the project so a policy stops depending on the layer it was found in.
func ProfileFile(sources Sources, name string) ([]byte, Profile, bool) {
	_, profile, _ := loadProfile(sources, name)
	if profile.Layer == "" {
		return nil, profile, false
	}
	if profile.Layer == LayerDefault {
		data, err := builtin.ReadFile(defaultsRoot + "/" + ProfileSubdir + "/" + name + ".json")
		return data, profile, err == nil
	}
	data, err := os.ReadFile(profile.Path)
	return data, profile, err == nil
}

func profilePath(dir, name string) string {
	if dir == "" || name == "" || strings.ContainsAny(name, `/\`) {
		return ""
	}
	return filepath.Join(dir, ProfileSubdir, name+".json")
}

// document is one decoded settings file, flattened to the dotted keys Fields names. One decoder
// reads both file kinds: a profile is a config.json under a name, and nothing here knows which
// of the two it is holding.
type document struct {
	layer, path string
	keys        map[string]any
	warnings    []string
}

// readFile decodes one settings file from disk. A file that is not there is not a layer.
func readFile(name, layer string) (document, bool) {
	return readDocument(os.DirFS(filepath.Dir(name)), filepath.Base(name), layer, name)
}

// readDocument decodes one settings file out of any filesystem, which is what lets the embedded
// defaults and the two on-disk layers share a decoder. A document that could not be read still
// carries the warning that says why, so no caller has to remember to collect it separately.
func readDocument(fsys fs.FS, name, layer, display string) (document, bool) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		if os.IsNotExist(err) {
			return document{layer: layer, path: display}, false
		}
		return document{layer: layer, path: display, warnings: []string{fmt.Sprintf("cannot read %s: %v, using the layers below it", display, err)}}, false
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return document{layer: layer, path: display, warnings: []string{fmt.Sprintf("%s is not a JSON object of settings, ignoring it", display)}}, false
	}
	return document{layer: layer, path: display, keys: flatten(raw)}, true
}

// applyTo merges this document's keys over values, recording the layer that won each one. An
// unusable setting is a warning and the layer below stands; nothing here can fail a run.
func (d document) applyTo(values *Values, layers map[string]string) []string {
	var warnings []string
	known := knownKeys()
	for _, key := range sortedKeys(d.keys) {
		if key == "description" {
			continue
		}
		if !known[key] {
			warnings = append(warnings, key+" is not a herdr-review-loop setting, ignoring it")
			continue
		}
		if err := setJSON(values, key, d.keys[key]); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v, using the value below it", d.describe(key), err))
			continue
		}
		if layers != nil {
			layers[key] = d.layer
		}
	}
	return warnings
}

// asProfile drops the one key a profile may not carry. A profile naming another profile is
// rejected rather than followed: profile chains are a resolution order nobody can hold in mind.
func (d document) asProfile() document {
	if _, chains := d.keys["profile"]; !chains {
		return d
	}
	keys := make(map[string]any, len(d.keys))
	for key, value := range d.keys {
		if key != "profile" {
			keys[key] = value
		}
	}
	d.keys = keys
	d.warnings = append(d.warnings, "a profile cannot select another profile, ignoring its profile key")
	return d
}

func (d document) description() string {
	text, _ := d.keys["description"].(string)
	return strings.TrimSpace(text)
}

func (d document) describe(key string) string {
	if d.path == "" {
		return key
	}
	return path.Base(d.path) + " " + key
}

func knownKeys() map[string]bool {
	known := make(map[string]bool, len(Fields())+len(structured))
	for _, field := range Fields() {
		known[field.Key] = true
	}
	for _, key := range structured {
		known[key] = true
	}
	return known
}

// flatten turns the nested file into the dotted keys Fields names. A group whose value is not an
// object is reported under its own name so the warning points at what the user actually wrote.
func flatten(raw map[string]any) map[string]any {
	nested := map[string]bool{}
	for _, group := range groups {
		nested[group] = true
	}
	flat := make(map[string]any, len(raw))
	for key, value := range raw {
		members, isObject := value.(map[string]any)
		if !nested[key] || !isObject {
			flat[key] = value
			continue
		}
		for member, memberValue := range members {
			flat[key+"."+member] = memberValue
		}
	}
	return flat
}

// sortedKeys keeps warning order stable across runs, since map iteration is not.
func sortedKeys(keys map[string]any) []string {
	names := make([]string, 0, len(keys))
	for name := range keys {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func dedupe(warnings []string) []string {
	if len(warnings) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(warnings))
	unique := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		if seen[warning] {
			continue
		}
		seen[warning] = true
		unique = append(unique, warning)
	}
	return unique
}
