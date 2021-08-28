package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
)

var (
	log      zerolog.Logger
	s        *discordgo.Session
	botToken string
	guildId  string
)

// init initializes logging for the program
// init first creates a (pretty-print ConsoleWriter) logger that writes to stdout to be used for logging events that occur while configuring the final logger. Configuring the final logger entails creating a file with name logName in directory logDir. Directory logDir is created if necessary and its permissions are set. If this all succeeds, init creates a logger based on a zerolog.MultiLevelWriter that is configured to log to stdout (as pretty-print) and to the aforementioned file (as JSON).
func init() {
	logDir := "log"
	logName := "log-" + fmt.Sprint(time.Now().Unix())
	logDirPermissions := fs.FileMode(0700)

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix // Unless overridden, loggers will output timestamps as Unix time

	consoleWriter := zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339} // RFC3389: Human-readable timestamps for console
	log = zerolog.New(consoleWriter).With().Timestamp().Logger()

	log.Debug().Msg("Console-only log initialized")

	// Create logDir drectory
	err := os.Mkdir(logDir, logDirPermissions)
	if err != nil {
		// If it already exists, there is no issue and we can continue
		if errors.Is(err, fs.ErrExist) {
			log.Debug().Msgf("'%s' directory already exists", logDir)
		} else { // Don't recover from any other errors. Treat this directory as a prerequisite that must be satisfied
			log.Fatal().Err(err).Msgf("Failed to create '%s' directory", logDir)
		}
	} else {
		log.Debug().Msgf("Created '%s' directory", logDir)
	}

	// Change permissions of logDir to logDirPermissions
	err = os.Chmod(logDir, logDirPermissions)
	if err != nil {
		log.Fatal().Err(err).Msgf("Failed to set specified permissions on '%s' directory", logDir)
	} else {
		log.Debug().Msgf("Set the permissions on '%s' to '%s'", logDir, logDirPermissions)
	}

	// Create a file logName in the logDir directory
	logPath := fmt.Sprintf("./%s/%s", logDir, logName)
	logFile, err := os.Create(logPath)
	if err != nil {
		log.Fatal().Err(err).Msgf("Failed to create log file '%s'", logPath)
	} else {
		log.Debug().Msgf("Created log file '%s'", logPath)
	}

	multiWriter := zerolog.MultiLevelWriter(consoleWriter, logFile)

	// Replace the current console-only logger with a new one based on a multi-writer
	log = zerolog.New(multiWriter).With().Timestamp().Logger()

	log.Debug().Msgf("Logger now writing to both standard out and '%s'", logPath) // Logs to console and file
}

func init() {
	var err error
	var is_present bool

	err = godotenv.Load()
	if err != nil {
		log.Warn().Msg("Could not load environment variables from file. Do they already exist in the environment?")
	} else {
		log.Info().Msg("Loaded environment values from file")
	}

	botToken, is_present = os.LookupEnv("botToken")
	if !is_present {
		log.Fatal().Msg("botToken environment value not found")
	}
	if len(botToken) == 0 {
		log.Fatal().Msg("The length of the botToken environment variable is zero")
	}

	guildId, is_present = os.LookupEnv("guildId")
	if !is_present {
		log.Fatal().Msg("guildId environment value not found")
	}
	if len(botToken) == 0 {
		log.Fatal().Msg("The length of the guildId environment variable is zero")
	}

	s, err = discordgo.New("Bot " + botToken)
	if err != nil {
		log.Fatal().Err(err).Msg("Invalid bot parameters")
	}
}

var (
	commands = []*discordgo.ApplicationCommand{
		{
			Name:        "img",
			Description: "Server-wide image gallery",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Name:        "random",
					Description: "Send a random image from the chosen gallery",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Name:        "gallery",
							Description: "The gallery to choose from",
							Type:        discordgo.ApplicationCommandOptionString,
							Required:    true,
							Choices: []*discordgo.ApplicationCommandOptionChoice{
								{
									Name:  "cheems",
									Value: "cheems",
								},
							},
						},
					},
				},
			},
		},
	}
	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"img": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			content := ""

			switch i.Type {
			case discordgo.InteractionApplicationCommand:
				command := i.ApplicationCommandData().Options[0]

				switch command.Name {
				case "random":
					option := command.Options[0]

					switch option.Value {
					case "cheems":
						content = "https://media.discordapp.net/attachments/621023220249657345/867856538798784563/image0.jpg"
					default:
						content = "Invalid gallery :stop_sign:\nHow did this happen?"
						logInteractionIssue(i.Interaction, "invalid gallery")
					}
				default:
					content = "Invalid subcommand :stop_sign:\nHow did this happen?"
					logInteractionIssue(i.Interaction, "invalid subcommand")
				}
			default:
				content = "I didn't expect to be interacted with in this way :flushed:\nPerhaps someone should look into this :thinking:"
				logInteractionIssue(i.Interaction, "unexpected interaction type")
			}

			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: content,
				},
			})
		},
	}
)

func logInteractionIssue(i *discordgo.Interaction, description string) {
	log.Warn().Msgf("Interaction issue: '%s'", description)
	var err error
	var interactionJsonBytes []byte
	var interactionDataJsonBytes []byte

	interactionJsonBytes, err = json.Marshal(i)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal interaction object")
	}

	interactionDataJsonBytes, err = json.Marshal(i.Data)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal interaction data object")
	}

	log.Warn().RawJSON("interaction", interactionJsonBytes).RawJSON("interaction_data", interactionDataJsonBytes).Msg("")
}

func init() {
	s.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if h, ok := commandHandlers[i.ApplicationCommandData().Name]; ok {
			h(s, i)
		}
	})
}

func main() {
	s.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		log.Info().Msg("Bot is up!")
	})
	err := s.Open()
	if err != nil {
		log.Fatal().Err(err).Msg("Cannot open the session")
	}

	for _, v := range commands {
		_, err := s.ApplicationCommandCreate(s.State.User.ID, guildId, v)
		if err != nil {
			log.Panic().Err(err).Msgf("Cannot create '%s' command", v.Name)
		}
	}

	defer s.Close()

	stop := make(chan os.Signal)
	signal.Notify(stop, os.Interrupt)
	<-stop
	log.Info().Msg("Exiting gracefully")
}
