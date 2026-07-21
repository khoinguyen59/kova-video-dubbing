package subtitlestyle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"golang.org/x/image/font/sfnt"
)

var installedFontIndex struct {
	once     sync.Once
	families []string
	paths    map[string]string
}

// SystemFontFamilies returns a deterministic, de-duplicated list of installed
// font *families*. Windows stores its system fonts in %WINDIR%\Fonts; the
// fallback directories keep the selector useful for portable Kova installs on
// macOS and Linux. Reading the OpenType name table avoids showing filenames
// such as "arialbd" as if they were user-facing family names.
func SystemFontFamilies() []string {
	installedFontIndex.once.Do(indexInstalledFonts)
	return append([]string(nil), installedFontIndex.families...)
}

// FindSystemFontFile resolves a user-facing font family to the installed font
// file Kova used while building its selector. Desktop preview uses this path
// to render a true font sample rather than silently falling back to Fyne's
// theme font. An empty return means the chosen family is not available now.
func FindSystemFontFile(family string) (string, bool) {
	installedFontIndex.once.Do(indexInstalledFonts)
	path, exists := installedFontIndex.paths[strings.ToLower(strings.TrimSpace(family))]
	return path, exists
}

func indexInstalledFonts() {
	directories := make([]string, 0, 4)
	if runtime.GOOS == "windows" {
		if windir := strings.TrimSpace(os.Getenv("WINDIR")); windir != "" {
			directories = append(directories, filepath.Join(windir, "Fonts"))
		}
		directories = append(directories, `C:\Windows\Fonts`)
	} else if runtime.GOOS == "darwin" {
		directories = append(directories, "/System/Library/Fonts", "/Library/Fonts")
	} else {
		directories = append(directories, "/usr/share/fonts", "/usr/local/share/fonts")
	}
	seen := map[string]struct{}{}
	paths := make(map[string]string)
	fonts := make([]string, 0, 128)
	for _, directory := range directories {
		_ = filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return nil
			}
			extension := strings.ToLower(filepath.Ext(entry.Name()))
			if extension != ".ttf" && extension != ".otf" && extension != ".ttc" {
				return nil
			}
			families := fontFamilies(path)
			if len(families) == 0 {
				// Keep a usable fallback for a corrupt or unusual local font file.
				families = []string{strings.TrimSpace(strings.TrimSuffix(entry.Name(), extension))}
			}
			for _, family := range families {
				if family != "" {
					key := strings.ToLower(family)
					if _, exists := seen[key]; !exists {
						seen[key] = struct{}{}
						fonts = append(fonts, family)
						paths[key] = path
					}
				}
			}
			return nil
		})
	}
	if len(fonts) == 0 {
		fonts = append(fonts, "Arial")
	}
	sort.Strings(fonts)
	installedFontIndex.families = fonts
	installedFontIndex.paths = paths
}

func fontFamilies(path string) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	collection, err := sfnt.ParseCollectionReaderAt(file)
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{})
	families := make([]string, 0, collection.NumFonts())
	for index := 0; index < collection.NumFonts(); index++ {
		face, faceErr := collection.Font(index)
		if faceErr != nil {
			continue
		}
		family, nameErr := face.Name(nil, sfnt.NameIDFamily)
		family = strings.TrimSpace(family)
		if nameErr == nil && family != "" {
			if _, exists := seen[family]; !exists {
				seen[family] = struct{}{}
				families = append(families, family)
			}
		}
	}
	return families
}

func SavePreset(directory, name string, set *StyleSet) (string, error) {
	name = sanitizePresetName(name)
	if name == "" {
		return "", fmt.Errorf("tên preset không hợp lệ")
	}
	if err := Validate(set); err != nil {
		return "", err
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(directory, name+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func ListPresets(directory string) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	presets := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() || strings.ToLower(filepath.Ext(entry.Name())) != ".json" {
			continue
		}
		presets = append(presets, strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
	}
	sort.Strings(presets)
	return presets, nil
}

func LoadPreset(directory, name string) (*StyleSet, error) {
	name = sanitizePresetName(name)
	if name == "" {
		return nil, fmt.Errorf("tên preset không hợp lệ")
	}
	return LoadOverrideFile(filepath.Join(directory, name+".json"))
}

func sanitizePresetName(name string) string {
	name = strings.TrimSpace(name)
	var output strings.Builder
	for _, character := range name {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' || character == ' ' {
			output.WriteRune(character)
		}
	}
	return strings.TrimSpace(output.String())
}

func Int(value int) *int           { return &value }
func Float(value float64) *float64 { return &value }
func Bool(value bool) *bool        { return &value }
