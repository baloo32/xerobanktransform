package main

import (
	"encoding/csv"
	"flag"
	"io"
	"os"
	"os/user"
	"strings"
	"time"

	logging "github.com/op/go-logging"
	"github.com/stretchr/slog"
)

// LOGGER is a logger to pass to certain functions that works with slog package only
var LOGGER = slog.New("transform", slog.ParseLevel("DEBUG"))

// Transform is a struct to output a CSV in the format required for Xero imports
type Transform struct {
	Date            string
	Amount          string
	Payee           string
	Description     string
	Reference       string
	ChequeNumber    string
	TransactionType string
}

var (
	// Logger settings
	log              = logging.MustGetLogger("xero-bank-transform")
	logConsoleFormat = logging.MustStringFormatter(
		`%{color}%{time:15:04:05.000} %{shortfunc} (%{shortfile}) >> %{message} %{color:reset}`,
	)
	logFileFormat = logging.MustStringFormatter(
		`%{time:15:04:05.000} %{shortfunc} (%{shortfile}) >> %{message}`,
	)

	// Path to log files
	logPath string
	// Enable console log
	outputConsole bool
	// CSV file to import
	csvImportPath string
	// CSV file to output
	csvOutputPath string

	// file to write console output into
	consoleLogFile *os.File

	csvTransactionsTotal int
)

func main() {
	log.Info("Bank Statements Transform tool")
	log.Info("Started at " + time.Now().UTC().String())
	log.Info("Parsing command line...")

	flag.StringVar(&csvImportPath, "file", "", "CSV file to read from")
	flag.StringVar(&csvOutputPath, "outfile", "", "CSV file to output to")
	flag.StringVar(&logPath, "logpath", "~/logs/xero-bank-transform", "Path to console log files")
	flag.BoolVar(&outputConsole, "outputconsole", true, "Enable console log")
	flag.Parse()

	log.Warningf("CSV import file - %s", csvImportPath)
	log.Warningf("CSV output file - %s", csvOutputPath)
	log.Warningf("Path to log files - %s", logPath)
	log.Warningf("Enable console log - %t", outputConsole)

	// Include timestamp into log file names
	timeNowStr := time.Now().UTC().Format("2006-01-02T15-04-05Z")

	consoleLogFileName := "console_" + timeNowStr + ".log"

	// Expand "~" to user home directory in log path
	usr, _ := user.Current()
	dir := usr.HomeDir
	logPath = strings.Replace(logPath, "~", dir, 1)

	// Create log path if it doesn't exist
	err := os.MkdirAll(logPath, 0777)
	// If unable to create the directory, terminate
	if err != nil {
		log.Fatal(err)
	}

	// Enable console log if needed
	if outputConsole {
		logConsoleBackend := logging.NewLogBackend(os.Stderr, "", 0)
		logConsolePrettyBackend := logging.NewBackendFormatter(logConsoleBackend, logConsoleFormat)

		consoleLogFile = createFile(logPath + "/" + consoleLogFileName)
		defer consoleLogFile.Close()

		logFileBackend := logging.NewLogBackend(consoleLogFile, "", 0)
		logFilePrettyBackend := logging.NewBackendFormatter(logFileBackend, logFileFormat)

		logging.SetBackend(logConsolePrettyBackend, logFilePrettyBackend)
	}

	// CSV Reader
	csvImportFile := openFile(csvImportPath)
	defer csvImportFile.Close()
	csvr := csv.NewReader(csvImportFile)

	csvOutputFile := createFile(csvOutputPath)
	defer csvOutputFile.Close()
	csvw := csv.NewWriter(csvOutputFile)

	var headers []string
	// Read header line
	for {
		row, err := csvr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		// There is extra guff in the export file, so only read the correct header
		if row[0] == " Date" && row[1] == "Description" {
			for _, heading := range row {
				if heading == " Date" {
					headers = append(headers, "Date")
					continue
				}
				if heading == "Bank     Reference" {
					headers = append(headers, "Bank Reference")
					continue
				}
				if heading == "Customer  Reference" {
					headers = append(headers, "Customer Reference")
					continue
				}
				if heading == "Running  Balance  " {
					headers = append(headers, "Running Balance")
					continue
				}
				headers = append(headers, heading)
			}
		}
		if len(headers) > 0 {
			break
		}
	}
	if len(headers) == 0 {
		log.Fatal("Unable to read header row")
	}
	log.Debugf("File headers: %s", headers)

	xeroCSVHeaders := []string{
		"*Date",
		"*Amount",
		"Payee",
		"Description",
		"Reference",
		"Cheque Number",
		"Transaction Type",
	}

	csvw.Write(xeroCSVHeaders)
	// Read transactions from CSV
	for {
		row, err := csvr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}

		data := map[string]string{}
		for i, v := range row {
			data[headers[i]] = v
		}

		log.Warningf("Next transaction: %s", data)
		if len(data["Date"]) == 0 || data["Date"] == "<nil>" {
			continue
		}
		if data["Date"] == "Transactions" {
			continue
		}
		if data["Date"] == " Date" {
			continue
		}
		csvTransactionsTotal++

		// Prepare Xero Transaction
		xeroTransaction := &Transform{
			Date:         data["Date"],
			Payee:        "",
			Description:  data["Customer Reference"],
			Reference:    data["Description"] + " " + data["Bank Reference"],
			ChequeNumber: "",
		}
		if data["Credit"] != "" && data["Credit"] != "<nil>" {
			xeroTransaction.Amount = data["Credit"]
			xeroTransaction.TransactionType = "Credit"
		}
		if data["Debit"] != "" && data["Debit"] != "<nil>" {
			xeroTransaction.Amount = "-" + data["Debit"]
			xeroTransaction.TransactionType = "Debit"
		}
		csvw.Write([]string{
			xeroTransaction.Date,
			xeroTransaction.Amount,
			xeroTransaction.Payee,
			xeroTransaction.Description,
			xeroTransaction.Reference,
			xeroTransaction.ChequeNumber,
			xeroTransaction.TransactionType,
		})
		csvw.Flush()
	}
	csvw.Flush()

	log.Warning("Transform completed")
	log.Noticef("%d total transactions found in CSV", csvTransactionsTotal)
	log.Info("Completed at " + time.Now().UTC().String())
}

// createFile creates new file
func createFile(path string) *os.File {
	if path == "" {
		return nil
	}

	fh, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}

	return fh
}

// openFile opens file
func openFile(path string) *os.File {
	if path == "" {
		return nil
	}

	fh, err := os.Open(path)
	if err != nil {
		log.Fatal(err)
	}

	return fh
}
