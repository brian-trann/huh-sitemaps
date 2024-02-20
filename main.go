package main

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/huh/spinner"
	"github.com/charmbracelet/lipgloss"
)

func main() {

	robotsURL := promptForRobots()

	sitemapUrls := getSitemapsFromRobots(robotsURL)
	sitemapOptions := promptSelectionAndType(sitemapUrls)
	// spinner be weird
	_ = spinner.New().Title("Processing sitemaps...").Type(spinner.Meter).Run()
	var wg sync.WaitGroup
	urlsChan := make(chan string, 100)

	wg.Add(1)
	go getUrlsFromSitemap(sitemapOptions.Selection, &wg, urlsChan)
	go func() {
		wg.Wait()
		close(urlsChan)

	}()

	printStyledSummary(sitemapOptions, urlsChan)
}
func promptForRobots() string {
	var urlInput string
	input := huh.NewInput().
		Title("Enter the URL to a robots.txt file:").
		Value(&urlInput).
		Validate(func(s string) error {
			// Check if the URL is valid
			parsedURL, err := url.ParseRequestURI(s)
			if err != nil {
				return fmt.Errorf("the provided string is not a valid URL")
			}

			if !strings.HasSuffix(parsedURL.Path, "robots.txt") {
				return fmt.Errorf("URL must end with 'robots.txt'")
			}
			return nil
		})

	if err := input.Run(); err != nil {
		fmt.Printf("Error prompting for URL: %v\n", err)
	}

	return urlInput
}

func promptSelectFromList(title string, options []string) string {
	var selection string

	huhOptions := make([]huh.Option[string], 0, len(options))
	for _, option := range options {
		huhOptions = append(huhOptions, huh.NewOption(option, option))
	}
	selectInput := huh.NewSelect[string]().
		Title(title).
		Options(huhOptions...).
		Value(&selection)

	if err := selectInput.Run(); err != nil {
		log.Fatalf("Failed to run select input: %v", err)
	}

	return selection
}
func getSitemapsFromRobots(url string) []string {
	resp, err := http.Get(url)
	if err != nil {
		panic(fmt.Sprintf("Error fetching robots.txt: %v", err))
	}
	defer resp.Body.Close()

	var sitemapURLs []string
	scanner := bufio.NewScanner(resp.Body)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "Sitemap: ") {
			sitemapURL := strings.TrimPrefix(line, "Sitemap: ")
			sitemapURLs = append(sitemapURLs, sitemapURL)
		}
	}

	if err := scanner.Err(); err != nil {
		panic(fmt.Sprintf("Error reading robots.txt: %v", err))
	}
	return sitemapURLs
}
func promptSelectionAndType(sitemaps []string) SitemapOptions {
	var selection, runType string

	sitemapOpts := make([]huh.Option[string], 0, len(sitemaps))
	for _, sitemap := range sitemaps {
		sitemapOpts = append(sitemapOpts, huh.NewOption(sitemap, sitemap))
	}
	sitemapSelect := huh.NewSelect[string]().
		Title("Select a sitemap.").
		Options(sitemapOpts...).
		Value(&selection)
	runTypeSelect := huh.NewSelect[string]().
		Title("Select a run type").
		Options(
			huh.NewOption("Get Total URLs", "total"),
			huh.NewOption("Get Pattern Match Total", "pattern"),
		).
		Value(&runType)
	group := huh.NewGroup(
		runTypeSelect,
		sitemapSelect,
	)
	form := huh.NewForm(group)
	if err := form.Run(); err != nil {
		log.Fatalf("Error running form: %v", err)
	}
	options := SitemapOptions{
		Selection: selection,
		RunType:   runType,
		Pattern:   "",
	}
	if runType == "pattern" {
		options.Pattern = promptPattern("What pattern do you want to match?")
	}
	return options

}
func promptPattern(title string) string {
	var pattern string
	huh.NewInput().
		Title(title).
		Value(&pattern).
		Run()
	return pattern
}

type SitemapOptions struct {
	Selection string
	RunType   string
	Pattern   string
}

func getUrlsFromSitemap(url string, wg *sync.WaitGroup, urlsChan chan<- string) {
	defer wg.Done()
	resp, err := http.Get(url)
	if err != nil {
		fmt.Println("Error fetching URL:", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("Error reading response body:", err)
		return
	}
	var sitemapIndex SitemapIndex
	var urlSet URLSet
	if xml.Unmarshal(body, &sitemapIndex) == nil {
		for _, sitemap := range sitemapIndex.Sitemaps {
			wg.Add(1)
			go getUrlsFromSitemap(sitemap.Loc, wg, urlsChan)
		}
	} else if xml.Unmarshal(body, &urlSet) == nil {
		for _, url := range urlSet.URLs {
			urlsChan <- url.Loc
		}
	}
}

type SitemapIndex struct {
	XMLName  xml.Name `xml:"sitemapindex"`
	Sitemaps []struct {
		Loc string `xml:"loc"`
	} `xml:"sitemap"`
}
type URLSet struct {
	XMLName xml.Name `xml:"urlset"`
	URLs    []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

func patternMatch(urlsChan <-chan string, pattern string) <-chan int {
	matchesChan := make(chan int, 1)
	lowerPattern := strings.ToLower(pattern)

	go func() {
		defer close(matchesChan)
		count := 0
		for url := range urlsChan {
			if strings.Contains(strings.ToLower(url), lowerPattern) {
				count++
			}
		}
		matchesChan <- count
	}()

	return matchesChan
}
func countURLs(urlsChan <-chan string) <-chan int {
	countChan := make(chan int, 1)
	go func() {
		defer close(countChan)
		count := 0
		for range urlsChan {
			count++
		}
		countChan <- count
	}()
	return countChan
}
func printStyledSummary(sitemapOptions SitemapOptions, urlsChan <-chan string) {
	var sb strings.Builder
	keywordStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	headerStyle := lipgloss.NewStyle().Bold(true)
	keyword := keywordStyle.Render
	fmt.Fprintf(&sb, "%s Selected sitemap: %s\n\n\n",
		headerStyle.Render("Sitemap Selection:"),
		keyword(sitemapOptions.Selection),
	)

	if sitemapOptions.RunType == "pattern" {
		matchesChan := patternMatch(urlsChan, sitemapOptions.Pattern)
		matchCount := <-matchesChan
		fmt.Fprintf(&sb, "%s %s matches found for pattern \"%s\".\n",
			headerStyle.Render("Pattern Match Summary:"),
			keyword(fmt.Sprint(matchCount)),
			keyword(sitemapOptions.Pattern),
		)
	} else if sitemapOptions.RunType == "total" {
		totalChan := countURLs(urlsChan)
		totalCount := <-totalChan
		fmt.Fprintf(&sb, "%s %s URLs found.\n",
			headerStyle.Render("Total URLs Summary:"),
			keyword(fmt.Sprint(totalCount)),
		)
	}

	// print summary
	fmt.Println(lipgloss.NewStyle().
		Width(80).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("63")).
		Padding(1, 2).
		Render(sb.String()),
	)
}
