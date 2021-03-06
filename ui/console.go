package ui

import (
	"flag"
	"fmt"
	"github.com/briandowns/spinner"
	"github.com/giwty/switch-library-manager/db"
	"github.com/giwty/switch-library-manager/process"
	"github.com/giwty/switch-library-manager/settings"
	"github.com/jedib0t/go-pretty/table"
	"go.uber.org/zap"
	"io/ioutil"
	"os"
	"strings"
	"time"
)

var (
	nspFolder = flag.String("f", "", "path to NSP folder")
	recursive = flag.Bool("r", true, "recursively scan sub folders")
	mode      = flag.String("m", "", "**deprecated**")
	s         = spinner.New(spinner.CharSets[26], 100*time.Millisecond)
)

type Console struct {
	baseFolder  string
	sugarLogger *zap.SugaredLogger
}

func CreateConsole(baseFolder string, sugarLogger *zap.SugaredLogger) *Console {
	return &Console{baseFolder: baseFolder, sugarLogger: sugarLogger}
}

func (c *Console) Start() {
	flag.Parse()

	if mode != nil && *mode != "" {
		fmt.Println("note : the mode option ('-m') is deprecated, please use the settings.json to control options.")
	}

	settingsObj := settings.ReadSettings(c.baseFolder)

	//1. load the titles JSON object
	fmt.Printf("Downlading latest switch titles json file")
	titleFile, titlesEtag, err := db.LoadAndUpdateFile(settings.TITLES_JSON_URL, settings.TITLE_JSON_FILENAME, settingsObj.TitlesEtag)
	if err != nil {
		fmt.Printf("title json file doesn't exist\n")
		return
	}
	settingsObj.TitlesEtag = titlesEtag

	//2. load the versions JSON object
	versionsFile, versionsEtag, err := db.LoadAndUpdateFile(settings.VERSIONS_JSON_URL, settings.VERSIONS_JSON_FILENAME, settingsObj.VersionsEtag)
	if err != nil {
		fmt.Printf("version json file doesn't exist\n")
		return
	}
	settingsObj.VersionsEtag = versionsEtag

	newUpdate, err := settings.CheckForUpdates(c.baseFolder)

	if newUpdate {
		fmt.Printf("\n=== New version available, download from Github ===\n")
	}

	//3. update the config file with new etag
	settings.SaveSettings(settingsObj, c.baseFolder)

	//4. create switch title db
	titlesDB, err := db.CreateSwitchTitleDB(titleFile, versionsFile)

	//5. read local files
	folderToScan := settingsObj.Folder
	if nspFolder != nil && *nspFolder != "" {
		folderToScan = *nspFolder
	}

	if folderToScan == "" {
		fmt.Printf("\n\nNo folder to scan was defined.\n")
		return
	}
	s.Restart()
	fmt.Printf("\n\nScanning folder [%v]", folderToScan)
	files, err := ioutil.ReadDir(folderToScan)
	if err != nil {
		fmt.Printf("\nfailed accessing NSP folder\n %v", err)
		return
	}

	keys, _ := settings.InitSwitchKeys(c.baseFolder)
	if keys == nil || keys.GetKey("header_key") == "" {
		fmt.Printf("\n!!NOTE!!: keys file was not found, deep scan is disabled, library will be based on file tags.\n %v", err)
	}

	recursiveMode := settingsObj.ScanRecursively
	if recursive != nil && *recursive != true {
		recursiveMode = *recursive
	}

	localDB, err := db.CreateLocalSwitchFilesDB(files, folderToScan, nil, recursiveMode)
	if err != nil {
		fmt.Printf("\nfailed to process local folder\n %v", err)
		return
	}

	fmt.Printf("\nFinished scan\n ")

	s.Stop()
	p := (float32(len(localDB.TitlesMap)) / float32(len(titlesDB.TitlesMap))) * 100

	fmt.Printf("Local library completion status: %.2f%% (have %d titles, out of %d titles)\n", p, len(localDB.TitlesMap), len(titlesDB.TitlesMap))

	if settingsObj.OrganizeOptions.DeleteOldUpdateFiles {
		s.Restart()
		fmt.Printf("\nDeleting old updates\n")
		process.DeleteOldUpdates(localDB)
		s.Stop()
	}

	if settingsObj.OrganizeOptions.RenameFiles || settingsObj.OrganizeOptions.CreateFolderPerGame {
		s.Restart()
		fmt.Printf("\nStarting library organization\n")
		process.OrganizeByFolders(folderToScan, localDB, titlesDB, nil)
		s.Stop()
	}

	if settingsObj.CheckForMissingUpdates {
		s.Restart()
		fmt.Printf("\nChecking for missing updates\n")
		processMissingUpdates(localDB, titlesDB)
		s.Stop()
	}

	if settingsObj.CheckForMissingDLC {
		s.Restart()
		fmt.Printf("\nChecking for missing DLC\n")
		processMissingDLC(localDB, titlesDB)
		s.Stop()
	}

	fmt.Printf("Completed")
}

func processMissingUpdates(localDB *db.LocalSwitchFilesDB, titlesDB *db.SwitchTitlesDB) {
	incompleteTitles := process.ScanForMissingUpdates(localDB.TitlesMap, titlesDB.TitlesMap)
	if len(incompleteTitles) != 0 {
		fmt.Print("\nFound available updates:\n\n")
	} else {
		fmt.Print("\nAll NSP's are up to date!\n\n")
		return
	}
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleColoredBright)
	t.AppendHeader(table.Row{"#", "Title", "TitleId", "Local version", "Latest Version", "Update Date"})
	i := 0
	for _, v := range incompleteTitles {
		t.AppendRow([]interface{}{i, v.Attributes.Name, v.Attributes.Id, v.LocalUpdate, v.LatestUpdate, v.LatestUpdateDate})
		i++
	}
	t.AppendFooter(table.Row{"", "", "", "", "Total", len(incompleteTitles)})
	t.Render()
}

func processMissingDLC(localDB *db.LocalSwitchFilesDB, titlesDB *db.SwitchTitlesDB) {
	incompleteTitles := process.ScanForMissingDLC(localDB.TitlesMap, titlesDB.TitlesMap)
	if len(incompleteTitles) != 0 {
		fmt.Print("\nFound missing DLCS:\n\n")
	} else {
		fmt.Print("\nYou have all the DLCS!\n\n")
		return
	}
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleColoredBright)
	t.AppendHeader(table.Row{"#", "Title", "TitleId", "Missing DLCs (titleId - Name)"})
	i := 0
	for _, v := range incompleteTitles {
		t.AppendRow([]interface{}{i, v.Attributes.Name, v.Attributes.Id, strings.Join(v.MissingDLC, "\n")})
		i++
	}
	t.AppendFooter(table.Row{"", "", "", "", "Total", len(incompleteTitles)})
	t.Render()
}
