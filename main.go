package main

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/andybalholm/cascadia"
	"github.com/mattn/godown"
	"github.com/otiai10/copy"
	"github.com/pkg/errors"
	"golang.org/x/net/html"
)

var method string

var stuffToRemove []string

var globalRegexesToReplace = map[*regexp.Regexp]string{
	regexp.MustCompile("\\n\\n\\n"): "\n\n",
}

var pandocRegexesToReplace = map[*regexp.Regexp]string{
	regexp.MustCompile("^:::.*$"):                  "",
	regexp.MustCompile("^``` .*"):                  "```",
	regexp.MustCompile("[{][.#][A-Za-z0-9-]+?[}]"): "",
	regexp.MustCompile("(\\W|^)\\[(.*)]"):          "__$2__",
}

var godownRegexesToReplace = map[*regexp.Regexp]string{
	regexp.MustCompile("^<div>$"): "",
}

func replaceAll(str string, regex *regexp.Regexp, repl string) string {
	var result strings.Builder
	result.Grow(len(str))
	scanner := bufio.NewScanner(strings.NewReader(str))
	for scanner.Scan() {
		result.WriteString(regex.ReplaceAllString(scanner.Text(), repl))
		result.WriteRune('\n')
	}

	if err := scanner.Err(); err != nil {
		panic(err)
	}

	return result.String()
}

func postProcessMarkdown(markdown string) string {
	if method == "pandoc" {
		markdown = strings.ReplaceAll(markdown, "\n\\\n", "\n")
	}
	for _, thingToRemove := range stuffToRemove {
		markdown = strings.ReplaceAll(markdown, thingToRemove, "")
	}
	var regexesToReplace map[*regexp.Regexp]string
	switch method {
	case "pandoc":
		regexesToReplace = pandocRegexesToReplace
	case "godown":
		regexesToReplace = godownRegexesToReplace
	}
	for regex, repl := range regexesToReplace {
		markdown = replaceAll(markdown, regex, repl)
	}
	for regex, repl := range globalRegexesToReplace {
		markdown = regex.ReplaceAllString(markdown, repl)
	}

	return markdown
}

func createElement(HTML string) *html.Node {
	elem, err := html.Parse(strings.NewReader(HTML))
	if err != nil {
		panic(err)
	}
	return elem
}

func preProcessHTML(node *html.Node) {
	var newAttrs []html.Attribute
	for _, attr := range node.Attr {
		keep := true

		switch attr.Key {
		case "id", "class", "style":
			keep = false
		case "href":
			if strings.HasPrefix(attr.Val, "http://") || strings.HasPrefix(attr.Val, "https://") {
				break
			}
			if strings.HasSuffix(attr.Val, ".html") {
				// Remove .html so that we have a proper reference
				attr.Val = strings.TrimSuffix(attr.Val, ".html")
			}
		}

		if keep {
			newAttrs = append(newAttrs, attr)
		}
	}
	node.Attr = newAttrs

	child := node.FirstChild
	for child != nil {
		preProcessHTML(child)
		child = child.NextSibling
	}
}

func processFilePandoc(filePath string, outputFile string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return errors.Wrap(err, "could not open file "+filePath)
	}
	HTML, err := html.Parse(f)
	if err != nil {
		return errors.Wrap(err, "could not parse HTML in "+filePath)
	}
	_ = f.Close()
	contentView := cascadia.MustCompile("#main-content > *").MatchAll(HTML)

	pandocCommand := exec.Command(
		"pandoc",
		"-r", "html", "-w", "markdown",
	)
	stdin, err := pandocCommand.StdinPipe()
	if err != nil {
		return errors.Wrap(err,
			"failed to open pipe to stdin of pandoc")
	}
	stdout, err := pandocCommand.StdoutPipe()
	if err != nil {
		return errors.Wrap(err,
			"failed to open pipe to stdout of pandoc")
	}
	err = pandocCommand.Start()
	if err != nil {
		return errors.Wrap(err,
			"could not start pandoc command with params: "+strings.Join(pandocCommand.Args, " "))
	}
	var markdownOutput string
	var readWaiter sync.WaitGroup
	readWaiter.Add(1)
	var readErr error
	go func() {
		var byt []byte
		byt, readErr = ioutil.ReadAll(stdout)
		if readErr == nil {
			markdownOutput = string(byt)
		}
		readWaiter.Done()
	}()

	for _, elem := range contentView {
		preProcessHTML(elem)
		err := html.Render(stdin, elem)
		if err != nil {
			fmt.Printf("Warning: failed to render element %v in file %s, is it malformed?\n%v\n",
				elem, filePath, err)
		}
	}
	_ = stdin.Close()
	readWaiter.Wait()

	if readErr != nil {
		return errors.Wrap(readErr, "failed reading from pandoc output")
	}

	err = ioutil.WriteFile(outputFile, []byte(postProcessMarkdown(markdownOutput)), 0640)
	if err != nil {
		return errors.Wrap(err, "failed writing file "+outputFile)
	}

	return nil
}

func processFileGodown(filePath string, outputFile string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return errors.Wrap(err, "could not open file "+filePath)
	}
	HTML, err := html.Parse(f)
	if err != nil {
		return errors.Wrap(err, "could not parse HTML in "+filePath)
	}
	_ = f.Close()
	contentView := cascadia.MustCompile("#main-content > *").MatchAll(HTML)

	var markdownOutput strings.Builder
	var htmlOutput strings.Builder

	for _, elem := range contentView {
		preProcessHTML(elem)
		err := html.Render(&htmlOutput, elem)
		if err != nil {
			fmt.Printf("Warning: failed to render element %v in file %s, is it malformed?\n%v\n",
				elem, filePath, err)
		}
	}

	err = godown.Convert(&markdownOutput, strings.NewReader(htmlOutput.String()), nil)
	if err != nil {
		return errors.Wrap(err, "failed to convert html to markdown")
	}

	err = ioutil.WriteFile(outputFile, []byte(postProcessMarkdown(markdownOutput.String())), 0640)
	if err != nil {
		return errors.Wrap(err, "failed writing file "+outputFile)
	}

	return nil
}

func main() {
	if len(os.Args) <= 2 {
		fmt.Printf("Usage: %s <source dir> <dest dir> [method]\n", os.Args[0])
		fmt.Println("Methods:")
		fmt.Println("\tpandoc")
		fmt.Println("\tgodown")
		os.Exit(1)
	}

	sourceDir := os.Args[1]
	if !DirectoryExists(sourceDir) {
		fmt.Printf("Source directory %s does not exist\n", sourceDir)
		os.Exit(1)
	}
	destDir := os.Args[2]
	if !DirectoryExists(destDir) {
		fmt.Printf("Destination directory %s does not exist\n", destDir)
		os.Exit(1)
	}

	if len(os.Args) == 4 {
		method = os.Args[3]
	} else {
		method = "pandoc"
	}

	fmt.Println("Using method", method)

	err := filepath.Walk(sourceDir, func(filePath string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(info.Name()), ".html") {
			return nil
		}
		abs, err := filepath.Abs(filePath)
		if err != nil {
			fmt.Println(err)
			return nil
		}
		relativeToSourceDir, err := filepath.Rel(sourceDir, filePath)
		if err != nil {
			fmt.Println(err)
			return nil
		}
		outputFile := path.Join(destDir, ReplaceExtension(relativeToSourceDir, "md"))
		if method == "pandoc" {
			err = processFilePandoc(abs, outputFile)
		} else {
			err = processFileGodown(abs, outputFile)
		}
		if err != nil {
			fmt.Printf("Failed to process file (%v) %s -> %s, skipping\n", err, abs, outputFile)
			return nil
		}
		fmt.Printf("File %s successfully processed, result: %s\n", filePath, outputFile)
		return nil
	})
	if err != nil {
		fmt.Printf("Error during walk: %v\n", err)
		os.Exit(1)
	}

	attachmentsDir := path.Join(sourceDir, "attachments")
	if DirectoryExists(attachmentsDir) {
		fmt.Println("Copying attachments...")
		err := copy.Copy(attachmentsDir, path.Join(destDir, "attachments"))
		if err != nil {
			fmt.Print(err)
			os.Exit(1)
		}
	}

	fmt.Println("Done.")
}
