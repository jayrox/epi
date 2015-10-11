package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/xmlpath.v2"
)

var (
	project            = "epi"
	apiKey             = "your thetvdb api key"
	flagdir            = flag.String("dir", "cwd", "directory to scan. default is current working directory (cwd)")
	flagminsize        = flag.Int64("min", 3000000, "minimum file size to include in scan. default is 3MB") // 3MB
	flaglanguage       = flag.String("l", "en", "language")                                                 // en
	flagseasonzero     = flag.Bool("s", false, "include season zero (specials, etc)")
	flagepisodezero    = flag.Bool("e", false, "include episode zero (specials, etc)")
	flagignore         = flag.String("i", "", "comma seperated list of show titles to ignore, please wrap in double quotes (\")")
	flagdebug          = flag.Bool("d", false, "show debug output")
	flagfuture         = flag.Int64("f", 365, "how many days into the future to include in the report. default is 365")
	flagpast           = flag.Int64("p", -1, "how many days into the past to include in the report. default is infinite")
	flagtba            = flag.Bool("t", false, "include episodes with a TBA (to be announced) air date")
	flagairdate        = flag.Bool("sa", false, "sort by air date (oldest to newest)")
	flagairdatereverse = flag.Bool("sar", false, "sort by air date (newest to oldest)")
	ef                 epiFlags
)

func main() {
	flag.Parse()
	// Print the logo :P
	printLogo()

	// Root folder to scan
	ef.Dir = flagString(flagdir)
	if ef.Dir == "cwd" {
		dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
		if err != nil {
			log.Fatal(err)
		}
		ef.Dir = dir
	}
	fmt.Printf("Scanning directory: %s\n", ef.Dir)

	// Min file size to parse
	// default is 3,000,000 bytes
	ef.Min = flagInt(flagminsize)
	printDebug("Min: %d\n", ef.Min)

	// Language to use when parsing
	// default is en
	ef.Language = flagString(flaglanguage)
	fmt.Printf("Language: %+v\n", ef.Language)

	// Include Season "0" typically these are specials
	ef.SeasonZero = flagBool(flagseasonzero)

	// Include Episode "0" typically these are specials
	ef.EpisodeZero = flagBool(flagepisodezero)

	// Get list of show titles to ignore
	ef.Ignore = normalize(flagString(flagignore))
	printDebug("Ignored shows: %s\n", ef.Ignore)

	// Get number of days into the future to include in the report
	// default is 365
	ef.Future = flagInt(flagfuture)

	// Get number of days into the past to include in the report
	// default is infinite
	ef.Past = flagInt(flagpast)

	// Include episodes with a TBA (to be announced) air date
	ef.TBA = flagBool(flagtba)

	// Sort by Air Date
	ef.SortAirDate = flagBool(flagairdate)

	// Sort by Air Date Reverse
	ef.SortAirDateReverse = flagBool(flagairdatereverse)

	// Get thetvdb.com mirror for season/episode data
	mirrorsurl := "http://thetvdb.com/api/" + apiKey + "/mirrors.xml"
	ef.Mirror = getMirrors(mirrorsurl)
	printDebug("Mirror: %s\n", ef.Mirror)

	// Below this line is missing episode data
	fmt.Println("__________________________________")

	// Holder of episode data
	episodes := episodes{}

	// Files listed under the root directory
	files, _ := folderFiles(ef.Dir)

	// 	Expected dirctory structure: Show Name/Season ##/Show Name - S##E## - Episode Title.ext
	episodes = folderWalk(files, episodes)

	// Sort Episodes by Air Date (oldest to newest)
	if ef.SortAirDate {
		sort.Sort(episodes)
	}

	// Sort Episodes by Air Date Reverse (newest to oldest)
	if ef.SortAirDateReverse {
		sort.Sort(sort.Reverse(episodes))
	}

	// Print a seperator line if debugging is enabled.
	printDebug("__________________________________", nil)

	// Print report
	printReport(episodes, ef)
}

// Hold flag data
type epiFlags struct {
	Mirror             string
	Dir                string
	Min                int64
	Language           string
	SeasonZero         bool
	EpisodeZero        bool
	Ignore             string
	Future             int64
	Past               int64
	TBA                bool
	SortAirDate        bool
	SortAirDateReverse bool
	Debug              bool
}

func flagString(fs *string) string {
	return fmt.Sprint(*fs)
}

func flagInt(fi *int64) int64 {
	return int64(*fi)
}

func flagBool(fb *bool) bool {
	return bool(*fb)
}

// Get list of files from the folder
func folderFiles(path string) (files []os.FileInfo, err error) {
	files, err = ioutil.ReadDir(path)
	if err != nil {
		ep := err.(*os.PathError)
		printDebug("Error: Invalid directory - %s\n", ep.Path)
		return nil, err
	}
	return files, nil
}

func isDirectory(path string) (bool, error) {
	fileInfo, err := os.Stat(path)
	return fileInfo.IsDir(), err
}

// Walk the list of files
func folderWalk(files []os.FileInfo, episodes []episode) []episode {
	for _, f := range files {
		showname := f.Name()
		printDebug("%+v\n", showname)
		if ignoreShow(ef.Ignore, showname) {
			printDebug("Ignoring: %s\n", showname)
			continue
		}
		showdir := ef.Dir + "\\" + showname
		seasons, _ := folderFiles(showdir)
		seriesname := ""
		seriesid := ""
		for _, season := range seasons {
			if season.Size() > 0 {
				continue
			}
			if !(strings.HasPrefix(season.Name(), "season") || strings.HasPrefix(season.Name(), "Season")) {
				continue
			}

			printDebug("* %+v\n", season.Name())
			seasondir := showdir + "\\" + season.Name()
			epis, _ := folderFiles(seasondir)
			for _, episode := range epis {
				if episode.Size() < ef.Min {
					continue
				}
				name := getName(episode.Name())
				if ignoreShow(ef.Ignore, normalize(name)) {
					printDebug("Ignoring: %s\n", name)
					continue
				}
				if seriesname != normalize(name) {
					seriesid = getSeries(name, ef.Mirror, ef.Language)
					if seriesid == "nil" {
						fmt.Printf("Error: Unable to find %s\n", name)
						continue
					}
					seriesname = normalize(name)

					printDebug("series name: %s\n", seriesname)
					printDebug("* * * Series ID: %s\n", seriesid)
					episodes = getSeriesInfo(name, seriesid, ef.Mirror, ef.Language, episodes)
				}

				seasonnum := getSeason(episode.Name())
				printDebug("* * * season: %d\n", seasonnum)

				index := 0
				loop := true
				for loop == true {
					episodenum, matches := getEpisode(episode.Name(), index)
					if episodenum == -1 || ((matches - 1) == index) {
						loop = false
					}
					printDebug("* * * episode: %d\n", episodenum)
					markHave(episodes, normalize(name), seasonnum, episodenum)
					index = index + 1
				}
			}
		}
	}
	return episodes
}

// Get show name from string
func getName(s string) (name string) {
	re, _ := regexp.Compile(`(^[a-zA-Z0-9'\.&() ]*)`)
	match := re.FindStringSubmatch(s)
	if len(match) != 0 {
		m := strings.TrimSpace(match[1])
		return m
	}
	return ""
}

// Get season number from string
func getSeason(s string) (season int64) {
	re, _ := regexp.Compile(`(S[0-9]{2})`)
	match := re.FindStringSubmatch(s)
	if len(match) != 0 {
		season, err := strconv.ParseInt(strings.TrimLeft(match[1], "sS"), 10, 0)
		if err != nil {
			fmt.Println(err)
		}
		return season
	}
	return 0
}

// Get episode number from string
func getEpisode(s string, index int) (episode int64, matches int) {
	re, _ := regexp.Compile(`(?i)(E[\d]{1,2})`)
	match := re.FindAllString(s, -1)
	matches = len(match)
	if matches != 0 {
		if index > (matches - 1) {
			return -1, matches
		}
		episode, err := strconv.ParseInt(strings.TrimLeft(match[index], "eE"), 10, 0)
		if err != nil {
			fmt.Println(err)
		}
		return episode, matches
	}
	return -1, matches
}

func getMirrors(url string) string {
	response, err := http.Get(url)
	if err != nil {
		fmt.Printf("%s", err)
		os.Exit(1)
	} else {
		defer response.Body.Close()
		path := xmlpath.MustCompile("/Mirrors/Mirror/mirrorpath")
		root, err := xmlpath.Parse(response.Body)
		if err != nil {
			fmt.Println("Err: ", err)
		}
		if value, ok := path.String(root); ok {
			return value
		}
	}
	return ""
}

func getURLorCache(fileName, url string) io.Reader {
	// Check Cache Folder; create if does not exist
	ok, err := exists("epi_cache")
	if ok == false || err != nil {
		os.MkdirAll("."+string(filepath.Separator)+"epi_cache", 0777)
	}
	var s []byte

	// Check if Cache File exists; if not get from web, store in cache
	ok, err = exists(fileName)
	if ok == false || err != nil {
		printDebug("getting from url: %s\n", url)
		response, err := http.Get(url)
		if err != nil {
			fmt.Printf("%s", err)
			os.Exit(1)
		} else {
			defer response.Body.Close()
		}

		// Convert to byte and write
		buf := new(bytes.Buffer)
		buf.ReadFrom(response.Body)
		s = buf.Bytes()
		err = ioutil.WriteFile(fileName, s, 0644)
		if err != nil {
			fmt.Println(err)
		}
	} else {
		// Cache exists, read and return
		printDebug("reading file: %s\n", fileName)
		s, err = ioutil.ReadFile(fileName)
		if err != nil {
			fmt.Println(err)
		}
	}

	return bytes.NewReader(s)
}

func getSeries(series, mirror, language string) string {
	// Cache file name
	fileName := fmt.Sprintf("epi_cache/%s_%s.xml", language, normalize(series))
	url := mirror + "/api/GetSeries.php?seriesname=" + url.QueryEscape(series) + "&language=" + language

	// Get XML data from file if exists or from URL
	xml := getURLorCache(fileName, url)

	// Parse XML data
	root, err := xmlpath.Parse(xml)
	if err != nil {
		fmt.Println("Error: ", err)
	}
	path := xmlpath.MustCompile("/Data/Series/seriesid")
	if value, ok := path.String(root); ok {
		return value
	}
	return "nil"
}

func getSeriesInfo(seriesname, seriesid, mirror, language string, episodes []episode) []episode {
	// Cache file name
	fileName := fmt.Sprintf("epi_cache/%s_%s_%s.xml", language, normalize(seriesname), seriesid)
	url := mirror + "/api/" + apiKey + "/series/" + seriesid + "/all/" + language + ".xml"

	// Get XML data from file if exists or from URL
	xml := getURLorCache(fileName, url)

	// Parse XML data
	root, err := xmlpath.Parse(xml)
	if err != nil {
		fmt.Println("Error: ", err)
	}
	spath := xmlpath.MustCompile("/Data/Series/Airs_Time")
	airtime, ok := spath.String(root)
	if !ok {
		printDebug("AirTime not defined\n", "")
	}

	path := xmlpath.MustCompile("/Data/Episode")
	iterator := path.Iter(root)
	for iterator.Next() {
		line := iterator.Node()

		s := getXpathInt("SeasonNumber", line)
		e := getXpathInt("EpisodeNumber", line)
		t := getXpathString("EpisodeName", line)
		// if the episode name is blank, set to TBA (to be announced)
		if t == "" {
			t = "TBA"
		}
		a := getXpathString("FirstAired", line)
		// if the air date is blank, set to TBA (to be announced)
		if a == "" {
			a = "TBA"
		}

		nname := normalize(seriesname)

		episode := episode{
			Name:           seriesname,
			NormalizedName: nname,
			Title:          t,
			Season:         s,
			Episode:        e,
			AirDate:        a,
			AirTime:        airtime,
			Have:           false,
		}
		episodes = append(episodes, episode)
	}
	return episodes
}

func getXpathString(xpath string, node *xmlpath.Node) string {
	path := xmlpath.MustCompile(xpath)
	if res, ok := path.String(node); ok {
		res = strings.Replace(res, "\u200b", "", -1) // WTF? Damn non-printable chars
		return strings.TrimSpace(res)
	}
	panic(fmt.Sprintf("No string found for %s", xpath))
}

func getXpathInt(xpath string, node *xmlpath.Node) int64 {
	str := getXpathString(xpath, node)
	i, err := strconv.Atoi(str)
	if err != nil {
		panic(err)
	}
	return int64(i)
}

// Mark the episode meta data as have
func markHave(episodes []episode, show string, season, episode int64) []episode {
	printDebug("* * * marking: %s - %d %d", show, season, episode)
	for i, v := range episodes {
		if v.NormalizedName == show {
			if v.Season == season {
				if v.Episode == episode {
					episodes[i].Have = true
					printDebug(" - marked", nil)
				}
			}
		}
	}
	printDebug("", nil)
	return episodes
}

// Holder of Episode data
type episode struct {
	Name           string
	NormalizedName string
	Title          string
	Season         int64
	Episode        int64
	AirDate        string
	AirTime        string
	Have           bool
}

type episodes []episode

func (slice episodes) Len() int {
	return len(slice)
}

func (slice episodes) Less(i, j int) bool {
	return slice[i].AirDate < slice[j].AirDate
}

func (slice episodes) Swap(i, j int) {
	slice[i], slice[j] = slice[j], slice[i]
}

type episodesByName []episode

func (slice episodesByName) Len() int {
	return len(slice)
}

func (slice episodesByName) Less(i, j int) bool {
	return slice[i].Name < slice[j].Name
}

func (slice episodesByName) Swap(i, j int) {
	slice[i], slice[j] = slice[j], slice[i]
}

// Normalize a string to lowercase, strip spaces, remove single and double quotes and periods
func normalize(s string) string {
	s = strings.ToLower(s)
	s = strings.Replace(s, " ", "", -1)
	s = strings.Replace(s, "'", "", -1)
	s = strings.Replace(s, "\"", "", -1)
	s = strings.Replace(s, ".", "", -1)
	return s
}

func countHaves(episodes []episode) (total, haves, missing int64) {
	haves = 0
	total = 0
	missing = 0
	for _, v := range episodes {
		if v.Have {
			haves = haves + 1
		} else {
			missing = missing + 1
		}

		total = total + 1
	}
	return total, haves, missing
}

func printReport(episodes []episode, ef epiFlags) {
	if len(episodes) == 0 {
		fmt.Println("No episodes found.")
		return
	}

	t, h, m := countHaves(episodes)
	fmt.Printf("Total Episodes: %d\n", t)
	fmt.Printf("Episodes in Library: %d\n", h)
	fmt.Printf("Missing Episodes: %d\n", m)
	fmt.Println("__________________________________")

	for _, v := range episodes {
		if !v.Have {
			// if the season number is 0 and the skip season zero is true, skip
			if v.Season == 0 && !ef.SeasonZero {
				continue
			}

			// if the episode number is 0 and skip episode zero is true, skip
			if v.Episode == 0 && !ef.EpisodeZero {
				continue
			}

			// if include episodes with TBA air date is false and the air date is false, skip
			if !ef.TBA && v.AirDate == "TBA" {
				continue
			}

			// if the past limit is set (> -1) and the air date is > than the past limit in days, skip
			if timeSince(v.AirDate) > ef.Past && ef.Past > -1 {
				continue
			}

			// if the future limit is set (> -1) and the air date is > than the future limit in days, skip
			if timeUntil(v.AirDate) > ef.Future && ef.Future > -1 {
				continue
			}
			fmt.Printf("%s - S%02dE%02d - %s -- %s @ %s\n", v.Name, v.Season, v.Episode, v.Title, v.AirDate, v.AirTime)
		}
	}
}

// Only print debug output if the debug flag is true
func printDebug(format string, vars ...interface{}) {
	if *flagdebug {
		if vars[0] == nil {
			fmt.Println(format)
			return
		}
		fmt.Printf(format, vars...)
	}
}

// Only include episodes if the show name is not set to be ignored
func ignoreShow(ignoredshows, showname string) bool {
	normalizedshowname := normalize(showname)
	printDebug("Ignore - %s:%s\n", ignoredshows, normalizedshowname)
	if strings.Contains(ignoredshows, normalizedshowname) {
		printDebug("Ignoring: %s\n", normalizedshowname)
		return true
	}
	return false
}

// Print the logo, obviously
func printLogo() {
	fmt.Println("███████╗██████╗ ██╗")
	fmt.Println("██╔════╝██╔══██╗╚═╝")
	fmt.Println("█████╗  ██████╔╝██╗")
	fmt.Println("██╔══╝  ██╔═══╝ ██║")
	fmt.Println("███████╗██║     ██║  Find Missing")
	fmt.Println("╚══════╝╚═╝     ╚═╝   TV Episodes")
	fmt.Println("")
}

const (
	// See http://golang.org/pkg/time/#Parse
	timeFormat = "2006-01-02"
)

// Time since now
func timeSince(d string) int64 {
	if d == "" {
		return -1
	}
	then, err := time.Parse(timeFormat, d)
	if err != nil {
		fmt.Println("Err: ", err)
		return -1
	}
	duration := time.Since(then)
	return int64(round(duration.Hours() / 24))
}

// Inverse of time since now
func timeUntil(d string) int64 {
	return -timeSince(d)
}

// Round the floats
func round(f float64) float64 {
	return math.Floor(f + .5)
}

// exists returns whether the given file or directory exists or not
func exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return true, err
}
