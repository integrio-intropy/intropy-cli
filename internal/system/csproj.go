package system

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// insertProjectReference inserts an ItemGroup referencing the shared
// contracts project immediately before the closing </Project> tag. The
// csproj was rendered by this run seconds earlier, so its shape is known;
// textual insertion preserves the template's comments and formatting,
// which encoding/xml round-tripping would destroy. include is
// slash-separated and relative to the csproj's directory.
func insertProjectReference(csprojPath, include string) error {
	data, err := os.ReadFile(csprojPath)
	if err != nil {
		return fmt.Errorf("reference shared contracts project: %w", err)
	}
	content := string(data)
	idx := strings.LastIndex(content, "</Project>")
	if idx < 0 {
		return fmt.Errorf("reference shared contracts project: rendered %s has no </Project> closing tag", filepath.Base(csprojPath))
	}

	// IsAspireProjectResource="false" keeps the Aspire AppHost from
	// treating the class library as an orchestrated resource.
	itemGroup := "    <ItemGroup>\n" +
		"        <!-- Shared contracts project, referenced by `intropy sys create`. -->\n" +
		"        <ProjectReference Include=\"" + include + "\" IsAspireProjectResource=\"false\" />\n" +
		"    </ItemGroup>\n\n"

	updated := content[:idx] + itemGroup + content[idx:]
	if err := os.WriteFile(csprojPath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("reference shared contracts project: %w", err)
	}
	return nil
}

// contractsInclude computes the ProjectReference Include path from the
// host's output directory to the shared library's csproj, slash-separated.
func contractsInclude(outputDir string, shared SharedLibrary) (string, error) {
	absOut, err := filepath.Abs(outputDir)
	if err != nil {
		return "", err
	}
	absShared, err := filepath.Abs(shared.Path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absOut, absShared)
	if err != nil {
		return "", fmt.Errorf("compute path from %s to shared contracts project %s: %w", outputDir, shared.Path, err)
	}
	return filepath.ToSlash(filepath.Join(rel, shared.Name+".csproj")), nil
}
