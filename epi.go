package main

import (
	"flag"
	"fmt"
	"gopkg.in/xmlpath.v2"
	"io/ioutil"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// http://thetvdb.com/wiki/index.php?title=Programmers_API
var api_key = "your thetvdb api key"

var flagdir = flag.String("dir", "./", "directory to scan")
var flagminsize = flag.Int64("min", 3000000, "directory to scan") // 3MB
var flaglanguage = flag.String("l", "en", "language")             // en
var flagseasonzero = flag.Bool("s", false, "include season zero (specials, etc)")
var flagepisodezero = flag.Bool("e", false, "include episode zero (specials, etc)")
var flagignore = flag.String("i", "", "comma seperated list of show titles to ignore, please wrap in double quotes (\")")
var flagdebug = flag.Bool("d", false, "show debug output")
var flagfuture = flag.Int64("f", 365, "how many days into the future to include in the report - default is 365")
var flagpast = flag.Int64("p", -1, "how many days into the past to include in the report - default is infinite")
var flagtba = flag.Bool("t", false, "include episodes with a TBA (to be announced) air date")

var EF EpiFlags

func main() {
	flag.Parse()
	// Print the logo :P
	PrintLogo()

	// Root folder to scan
	EF.Dir = FlagString(flagdir)
	fmt.Printf("Scanning directory: %s\n", EF.Dir)

	// Min file size to parse
	// default is 3,000,000 bytes
	EF.Min = FlagInt(flagminsize)
	PrintDebug("Min: %d\n", EF.Min)

	// Language to use when parsing
	// default is en
	EF.Language = FlagString(flaglanguage)
	fmt.Printf("Language: %+v\n", EF.Language)

	// Include Season "0" typically these are specials
	EF.SeasonZero = FlagBool(flagseasonzero)

	// Include Episode "0" typically these are specials
	EF.EpisodeZero = FlagBool(flagepisodezero)

	// Get list of show titles to ignore
	EF.Ignore = Normalize(FlagString(flagignore))
	PrintDebug("ignored shows: %s\n", EF.Ignore)

	// Get number of days into the future to include in the report
	// default is 365
	EF.Future = FlagInt(flagfuture)

	// Get number of days into the past to include in the report
	// default is infinite
	EF.Past = FlagInt(flagpast)

	// Include episodes with a TBA (to be announced) air date
	EF.TBA = FlagBool(flagtba)

	// Get thetvdb.com mirror for season/episode data
	mirrorsurl := "http://thetvdb.com/api/" + api_key + "/mirrors.xml"
	EF.Mirror = GetMirrors(mirrorsurl)
	PrintDebug("Mirror: %s\n", EF.Mirror)

	// Below this line is missing episode data
	fmt.Println("______________________________________")

	// Holder of episode data
	episodes := []Episode{}

	// Files listed under the root directory
	files, _ := FolderFiles(EF.Dir)

	// 	Expected dirctory structure: Show Name/Season ##/Show Name - S##E## - Episode Title.ext
	episodes = FolderWalk(files, episodes)
	PrintReport(episodes, EF)
}

// Hold flag data
type EpiFlags struct {
	Mirror string
	Dir string
	Min int64
	Language string
	SeasonZero bool
	EpisodeZero bool
	Ignore string
	Future int64
	Past int64
	TBA bool
	Debug bool
}

func FlagString(fs *string) string {
	return fmt.Sprint(*fs)
}

func FlagInt(fi *int64) int64 {
	return int64(*fi)
}

func FlagBool(fb *bool) bool {
	return bool(*fb)
}

// Get list of files from the folder
func FolderFiles(dir string) (files []os.FileInfo, err error) {
	files, err = ioutil.ReadDir(dir)
	if err != nil {
		ep := err.(*os.PathError)
		fmt.Printf("Error: Invalid directory - %s\n", ep.Path)
		return nil, err
	}
	return files, nil
}

// Walk the list of files
func FolderWalk(files []os.FileInfo, episodes []Episode) []Episode{
	for _, f := range files {
		showname := f.Name()
		PrintDebug("%+v\n", showname)
		if IgnoreShow(EF.Ignore, showname) {
			PrintDebug("Ignoring: %s\n", showname)
			continue
		}
		showdir := EF.Dir + "\\" + showname
		seasons, _ := FolderFiles(showdir)
		seriesname := ""
		seriesid := ""
		for _, season := range seasons {
			if season.Size() > 0 {
				continue
			}
			if !(strings.HasPrefix(season.Name(), "season") || strings.HasPrefix(season.Name(), "Season")) {
				continue
			}

			PrintDebug("* %+v\n", season.Name())
			seasondir := showdir + "\\" + season.Name()
			epis, _ := FolderFiles(seasondir)
			for _, episode := range epis {
				if episode.Size() < EF.Min {
					continue
				}
				name := GetName(episode.Name())
				if IgnoreShow(EF.Ignore, Normalize(name)) {
					PrintDebug("Ignoring: %s\n", name)
					continue
				}
				if seriesname != Normalize(name) {
					seriesid = GetSeries(name, EF.Mirror, EF.Language, api_key)
					if seriesid == "nil" {
						fmt.Printf("Error: Unable to find %s\n", name)
						continue
					}
					seriesname = Normalize(name)

					PrintDebug("series name: %s\n", seriesname)
					PrintDebug("* * * Series ID: %s\n", seriesid)
					episodes = GetSeriesInfo(name, seriesid, EF.Mirror, EF.Language, api_key, episodes)
				}

				seasonnum := GetSeason(episode.Name())
				PrintDebug("* * * season: %d\n", seasonnum)

				episodenum := GetEpisode(episode.Name())
				PrintDebug("* * * episode: %d\n", episodenum)
				MarkHave(episodes, Normalize(name), seasonnum, episodenum)
			}
		}
	}
	return episodes
}

// Get show name from string
func GetName(s string) (name string) {
	re, _ := regexp.Compile(`(^[a-zA-Z0-9'\.&() ]*)`)
	match := re.FindStringSubmatch(s)
	if len(match) != 0 {
		m := strings.TrimSpace(match[1])
		return m
	}
	return ""
}

// Get season number from string
func GetSeason(s string) (season int64) {
	re, _ := regexp.Compile(`(S[0-9]{2})`)
	match := re.FindStringSubmatch(s)
	if len(match) != 0 {
		season, err := strconv.ParseInt(strings.TrimLeft(match[1], "S"), 10, 0)
		if err != nil {
			fmt.Println(err)
		}
		return season
	}
	return 0
}

// Get episode number from string
func GetEpisode(s string) (episode int64) {
	re, _ := regexp.Compile(`(E[0-9]{2})`)
	match := re.FindStringSubmatch(s)
	if len(match) != 0 {
		episode, err := strconv.ParseInt(strings.TrimLeft(match[1], "E"), 10, 0)
		if err != nil {
			fmt.Println(err)
		}
		return episode
	}
	return 0
}

func GetMirrors(url string) string {
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

func GetSeries(series, mirror, language, api_key string) string {
	url := mirror + "/api/GetSeries.php?seriesname=" + url.QueryEscape(series) + "&language=" + language
	PrintDebug("%s\n", url)
	response, err := http.Get(url)
	if err != nil {
		fmt.Printf("%s", err)
		os.Exit(1)
	} else {
		defer response.Body.Close()
		path := xmlpath.MustCompile("/Data/Series/seriesid")
		root, err := xmlpath.Parse(response.Body)
		if err != nil {
			fmt.Println("Error: ", err)
		}
		if value, ok := path.String(root); ok {
			return value
		}
	}
	return "nil"
}

func GetSeriesInfo(seriesname, seriesid, mirror, language, api_key string, episodes []Episode) []Episode {
	url := mirror + "/api/" + api_key + "/series/" + seriesid + "/all/" + language + ".xml"
	PrintDebug("%s\n", url)
	response, err := http.Get(url)
	if err != nil {
		fmt.Printf("%s", err)
		os.Exit(1)
	} else {
		defer response.Body.Close()
		path := xmlpath.MustCompile("/Data/Episode")
		root, err := xmlpath.Parse(response.Body)
		if err != nil {
			fmt.Println("Error: ", err)
		}
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

			nname := Normalize(seriesname)

			episode := Episode{Name: seriesname, NormalizedName: nname, Title: t, Season: s, Episode: e, AirDate: a, Have: false}
			episodes = append(episodes, episode)
		}
		return episodes

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
func MarkHave (episodes []Episode, show string, season, episode int64) []Episode {
	PrintDebug("* * * marking: %s - %d %d", show, season, episode)
	for i, v := range episodes {
		if v.NormalizedName == show {
			if v.Season == season {
				if v.Episode == episode {
					episodes[i].Have = true
					PrintDebug(" - marked")
				}
			}
		}
	}
	PrintDebug("\n")
	return episodes
}

// Holder of Episode data
type Episode struct {
	Name string
	NormalizedName string
	Title string
	Season int64
	Episode int64
	AirDate string
	Have bool
}

// Normalize a string to lowercase, strip spaces, remove single and double quotes and periods
func Normalize(s string) string {
	s = strings.ToLower(s)
	s = strings.Replace(s, " ", "" , -1)
	s = strings.Replace(s, "'", "", -1)
	s = strings.Replace(s, "\"", "", -1)
	s = strings.Replace(s, ".", "", -1)
	return s
}

func PrintReport(episodes []Episode, EF EpiFlags) {
	for _, v := range episodes {
		if ! v.Have {
			// if the season number is 0 and the skip season zero is true, skip
			if v.Season == 0 && ! EF.SeasonZero {
				continue
			}

			// if the episode number is 0 and skip episode zero is true, skip
			if v.Episode == 0 && ! EF.EpisodeZero {
				continue
			}

			// if include episodes with TBA air date is false and the air date is false, skip
			if ! EF.TBA && v.AirDate == "TBA" {
				continue
			}

			// if the past limit is set (> -1) and the air date is > than the past limit in days, skip
			if TimeSince(v.AirDate) > EF.Past && EF.Past > -1 {
				continue
			}

			// if the future limit is set (> -1) and the air date is > than the future limit in days, skip
			if TimeUntil(v.AirDate) > EF.Future && EF.Future > -1 {
				continue
			}
			fmt.Printf("%s - S%02dE%02d - %s -- %s\n", v.Name, v.Season, v.Episode, v.Title, v.AirDate)
		}
	}
}

// Only print debug output if the debug flag is true
func PrintDebug(format string, vars ...interface{}) {
	if *flagdebug {
		fmt.Printf(format, vars...)
	}
}

// Only include episodes if the show name is not set to be ignored
func IgnoreShow(ignoredshows, showname string) bool {
	normalizedshowname := Normalize(showname)
	PrintDebug("Ignore - %s:%s\n", ignoredshows, normalizedshowname)
	if strings.Contains(ignoredshows, normalizedshowname) {
		PrintDebug("Ignoring: %s\n", normalizedshowname)
		return true
	}
	return false
}

// Print the logo, obviously
func PrintLogo() {
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
func TimeSince(d string) int64 {
	if d == "" {
		return -1
	}
	then, err := time.Parse(timeFormat, d)
    if err != nil {
        fmt.Println("Err: ", err)
        return -1
    }
    duration := time.Since(then)
    return int64(Round(duration.Hours() / 24))
}

// Inverse of time since now
func TimeUntil(d string) int64 {
	return -TimeSince(d)
}

// Round the floats
func Round(f float64) float64 {
    return math.Floor(f + .5)
}
