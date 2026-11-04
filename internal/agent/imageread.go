package agent

// read_image: look at an image file in the workspace. DSH's executor has this tool
// for visual tasks; our model endpoint is text-only, so this tool reports honest
// metadata instead of pretending to see the pixels. The observation names what is
// missing and how to proceed, so a Worker can still make progress (e.g. run an
// image-to-text command with bash, or ask the person).

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"io/fs"
	"os"
	"strings"

	// Register the decoders image.DecodeConfig uses. All are pure Go.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

const (
	// readImageMaxBytes bounds the file read; an image bigger than this is refused
	// rather than loaded into memory.
	readImageMaxBytes = 10 << 20
	readImageToolName = "read_image"
)

const readImageDesc = "Read an image file from the workspace and report its format, size and dimensions. " +
	"Use it to confirm what a visual asset is before working with it. Paths outside the workspace are refused."

type readImageArgs struct {
	Path string `json:"path" jsonschema:"description=path to an image file relative to the workspace root"`
}

func newReadImageTool(root string) (tool.BaseTool, error) {
	return utils.InferTool(readImageToolName, readImageDesc,
		func(_ context.Context, in readImageArgs) (string, error) {
			return readImageObservation(root, in.Path), nil
		})
}

// readImageObservation is the whole tool body, split out so it can be tested
// without the tool wrapper. Refusals are text observations: a bad path must not
// abort the turn.
func readImageObservation(root, path string) string {
	local := &localBackend{root: root}
	rel, err := local.rootPath(path)
	if err != nil {
		return "refused: " + err.Error()
	}
	var info fs.FileInfo
	err = local.withRoot(func(r *os.Root) error {
		fi, statErr := r.Stat(rel)
		info = fi
		return statErr
	})
	if errors.Is(err, fs.ErrNotExist) {
		return "refused: no image file at " + path
	}
	if err != nil {
		return "refused: cannot read " + path + ": " + err.Error()
	}
	if info.IsDir() {
		return "refused: " + path + " is a directory, not an image"
	}
	if info.Size() > readImageMaxBytes {
		return fmt.Sprintf("refused: %s is %d bytes; read_image accepts files up to %d MiB",
			path, info.Size(), readImageMaxBytes>>20)
	}
	var data []byte
	err = local.withRoot(func(r *os.Root) error {
		f, openErr := r.Open(rel)
		if openErr != nil {
			return openErr
		}
		defer f.Close()
		var readErr error
		data, readErr = io.ReadAll(io.LimitReader(f, readImageMaxBytes+1))
		return readErr
	})
	if err != nil {
		return "refused: cannot read " + path + ": " + err.Error()
	}
	if len(data) > readImageMaxBytes {
		return fmt.Sprintf("refused: %s is larger than %d MiB", path, readImageMaxBytes>>20)
	}
	format, width, height, ok := imageInfo(data)
	if !ok {
		return "refused: " + path + " is not a supported image (png, jpeg, gif or webp)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\nformat: %s\nsize: %d bytes\n", path, format, len(data))
	if width > 0 && height > 0 {
		fmt.Fprintf(&b, "dimensions: %dx%d\n", width, height)
	}
	b.WriteString("content: image pixels are NOT available to this model\n")
	b.WriteString("note: this Worker's model endpoint is text-only in this build, so no image content is attached " +
		"to the request. To extract visual information, run a local converter/OCR with bash (for example " +
		"`file`, ImageMagick `identify`, or a tesseract OCR run) and read its text output; if the task needs a " +
		"person to look at the image, use ask_user_question and present the path.")
	return b.String()
}

// imageInfo detects the format and, where a pure-Go decoder exists, the dimensions.
// webp is recognised by its RIFF header but dimensions are not decoded here.
func imageInfo(data []byte) (format string, width, height int, ok bool) {
	cfg, f, err := image.DecodeConfig(bytes.NewReader(data))
	if err == nil {
		return f, cfg.Width, cfg.Height, true
	}
	if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "webp", 0, 0, true
	}
	return "", 0, 0, false
}
