package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	log             zerolog.Logger
	s               *discordgo.Session
	firestoreClient *firestore.Client
	ctx             = context.Background()
	config          = map[string]string{
		"botToken":                         "",
		"guildId":                          "",
		"googleApplicationCredentialsPath": "",
		"projectId":                        "",
	}
)

type Gallery struct {
	Images []map[string]string `firestore:"images"`
}

// Initialize rand (with current time)
func init() {
	rand.Seed(time.Now().UnixNano())
}

// Initialize logging
// It first creates a (pretty-print ConsoleWriter) logger that writes to stdout to be used for logging events that occur while configuring the final logger. Configuring the final logger entails creating a file with name logName in directory logDir. Directory logDir is created if necessary and its permissions are set. If this all succeeds, init creates a logger based on a zerolog.MultiLevelWriter that is configured to log to stdout (as pretty-print) and to the aforementioned file (as JSON).
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

// Initalize environment
func init() {
	var err error
	var isPresent bool

	err = godotenv.Load()
	if err != nil {
		log.Warn().Msg("Could not load environment variables from file. Do they already exist in the environment?")
	} else {
		log.Info().Msg("Loaded environment values from file")
	}

	for key, val := range config {
		val, isPresent = os.LookupEnv(key)
		if !isPresent {
			log.Fatal().Msgf("Environment value '%s' is missing", key)
		}
		if len(val) == 0 {
			log.Fatal().Msgf("Environment value '%s' is present but empty", key)
		}
		config[key] = val
	}
}

func populateGalleryChoices() (options []*discordgo.ApplicationCommandOptionChoice) {
	galleries, err := firestoreClient.Collection("galleries").DocumentRefs(ctx).GetAll()
	if err != nil {
		log.Error().Err(err).Caller().Msg("Failed to get DocumentRefs from Firestore")
	}
	log.Debug().Msgf("Found %d galleries", len(galleries))
	for _, v := range galleries {
		// log.Debug().Msgf("Found gallery '%s'", v.ID)
		options = append(options,
			&discordgo.ApplicationCommandOptionChoice{
				Name:  v.ID,
				Value: v.ID,
			},
		)
	}
	return options
}

func getGalleryDocRef(galleryName string) (docRef *firestore.DocumentRef) {
	docRef = firestoreClient.Collection("galleries").Doc(galleryName)
	return docRef
}

func getRandomImageFromGallery(i *discordgo.Interaction) (data discordgo.InteractionResponseData) {
	var embed discordgo.MessageEmbed

	command := i.ApplicationCommandData().Options[0]
	galleryName := command.Options[0].StringValue()

	docRef := getGalleryDocRef(galleryName)
	if docRef != nil {
		docSnap, err := docRef.Get(ctx)
		if err != nil {
			log.Error().Err(err).Caller().Interface("interaction", i).Interface("docSnap", docSnap).Msg("Failed to retrieve document contents")
			embed = discordgo.MessageEmbed{
				Description: "Unable to get gallery contents :stop_sign:",
				Color:       0xf04747,
			}
			data.Embeds = []*discordgo.MessageEmbed{&embed}
			return data
		}
		var gallery Gallery
		err = docSnap.DataTo(&gallery)
		if err != nil {
			log.Error().Err(err).Caller().Interface("interaction", i).Interface("docSnap", docSnap).Msg("Failed to retrieve document contents")
			embed = discordgo.MessageEmbed{
				Description: "Unable to get gallery contents :stop_sign:",
				Color:       0xf04747,
			}
			data.Embeds = []*discordgo.MessageEmbed{&embed}
			return data
		}
		images := gallery.Images
		// log.Debug().Interface("gallery", gallery).Interface("images", images).Msg("")
		numberOfImages := len(images)
		if numberOfImages > 0 {
			if numberOfImages == 1 {
				embed = discordgo.MessageEmbed{
					Image: &discordgo.MessageEmbedImage{
						URL: images[0]["imageUrl"],
					},
					Footer: &discordgo.MessageEmbedFooter{
						Text: fmt.Sprintf("Image: %d of %d | Gallery: %s", 0, numberOfImages-1, galleryName),
					},
				}
			} else {
				chosenImageInt := rand.Intn(numberOfImages)
				embed = discordgo.MessageEmbed{
					Image: &discordgo.MessageEmbedImage{
						URL: images[chosenImageInt]["imageUrl"],
					},
					Footer: &discordgo.MessageEmbedFooter{
						Text: fmt.Sprintf("Image: %d of %d | Gallery: %s", chosenImageInt, numberOfImages-1, galleryName),
					},
				}
			}
		} else {
			embed = discordgo.MessageEmbed{
				Description: "Gallery is empty :stop_sign:",
				Color:       0xf04747,
			}
			log.Debug().Msg("Attempted image retrieval from empty gallery")
		}
	} else {
		embed = discordgo.MessageEmbed{
			Description: "Gallery does not exist :stop_sign:",
			Color:       0xf04747,
		}
		log.Warn().Interface("interaction", i).Msg("Attempted image retrieval from non-existent gallery")
	}
	data.Embeds = []*discordgo.MessageEmbed{&embed}
	return data
}

func getImageFromGallery(i *discordgo.Interaction) (data discordgo.InteractionResponseData) {
	var embed discordgo.MessageEmbed

	command := i.ApplicationCommandData().Options[0]
	galleryName := command.Options[0].StringValue()
	imageNum := int(command.Options[1].IntValue())

	docRef := getGalleryDocRef(galleryName)
	if docRef != nil {
		docSnap, err := docRef.Get(ctx)
		if err != nil {
			log.Error().Err(err).Caller().Interface("interaction", i).Interface("docSnap", docSnap).Msg("Failed to retrieve document contents")
			embed = discordgo.MessageEmbed{
				Description: "Unable to get gallery contents :stop_sign:",
				Color:       0xf04747,
			}
			data.Embeds = []*discordgo.MessageEmbed{&embed}
			return data
		}
		var gallery Gallery
		err = docSnap.DataTo(&gallery)
		if err != nil {
			log.Error().Err(err).Caller().Interface("interaction", i).Interface("docSnap", docSnap).Msg("Failed to retrieve document contents")
			embed = discordgo.MessageEmbed{
				Description: "Unable to get gallery contents :stop_sign:",
				Color:       0xf04747,
			}
			data.Embeds = []*discordgo.MessageEmbed{&embed}
			return data
		}
		images := gallery.Images
		numberOfImages := len(images)
		if numberOfImages > 0 {
			if imageNum < 0 || imageNum >= numberOfImages {
				if numberOfImages == 1 {
					embed = discordgo.MessageEmbed{
						Description: "Invalid image number :stop_sign:\n(Only image number 0 is valid. Perhaps add more images to the gallery?)",
						Color:       0xf04747,
					}
				} else {
					embed = discordgo.MessageEmbed{
						Description: fmt.Sprintf("Invalid image number :stop_sign: (Valid image numbers include 0 through %d inclusive.)", numberOfImages-1),
						Color:       0xf04747,
					}
				}
			} else {
				embed = discordgo.MessageEmbed{
					Image: &discordgo.MessageEmbedImage{
						URL: images[imageNum]["imageUrl"],
					},
					Footer: &discordgo.MessageEmbedFooter{
						Text: fmt.Sprintf("Image: %d of %d | Gallery: %s", imageNum, numberOfImages-1, galleryName),
					},
				}
			}
		} else {
			embed = discordgo.MessageEmbed{
				Description: "Gallery is empty :stop_sign:",
				Color:       0xf04747,
			}
			log.Debug().Msg("Attempted image retrieval from empty gallery")
		}
	} else {
		embed = discordgo.MessageEmbed{
			Description: "Gallery does not exist :stop_sign:",
			Color:       0xf04747,
		}
		log.Warn().Interface("interaction", i).Msg("Attempted image retrieval from non-existent gallery")
	}
	data.Embeds = []*discordgo.MessageEmbed{&embed}
	return data
}

func addImageToGallery(i *discordgo.Interaction) (data discordgo.InteractionResponseData) {
	var embed discordgo.MessageEmbed

	command := i.ApplicationCommandData().Options[0]
	galleryName := command.Options[0].StringValue()
	imageUrl := command.Options[1].StringValue()
	timestamp := fmt.Sprint(time.Now().Unix())
	authorId := i.Member.User.ID

	docRef := getGalleryDocRef(galleryName)
	if docRef != nil {
		docSnap, err := docRef.Get(ctx)
		if err != nil {
			log.Error().Err(err).Caller().Interface("interaction", i).Interface("docSnap", docSnap).Msg("Failed to retrieve document contents")
			embed = discordgo.MessageEmbed{
				Description: "Unable to get gallery contents :stop_sign:",
				Color:       0xf04747,
			}
			data.Embeds = []*discordgo.MessageEmbed{&embed}
			return data
		}
		var gallery Gallery
		err = docSnap.DataTo(&gallery)
		if err != nil {
			log.Error().Err(err).Caller().Interface("interaction", i).Interface("docSnap", docSnap).Msg("Failed to retrieve document contents")
			embed = discordgo.MessageEmbed{
				Description: "Unable to get gallery contents :stop_sign:",
				Color:       0xf04747,
			}
			data.Embeds = []*discordgo.MessageEmbed{&embed}
			return data
		}
		// TODO: Validate the given imageUrl (length, format, expected params, etc.)
		gallery.Images = append(gallery.Images, map[string]string{
			"imageUrl":  imageUrl,
			"timestamp": timestamp,
			"authorId":  authorId,
		})
		_, err = docRef.Set(ctx, gallery)
		if err != nil {
			log.Error().Err(err).Caller().Interface("interaction", i).Interface("DocRef", docRef).Msg("Failed to write document contents")
			embed = discordgo.MessageEmbed{
				Description: "Unable to modify gallery contents :stop_sign:",
				Color:       0xf04747,
			}
			data.Embeds = []*discordgo.MessageEmbed{&embed}
			return data
		} else {
			log.Debug().Str("imageUrl", imageUrl).Str("user", i.Member.User.Username).Str("gallery", galleryName).Msg("Image added to gallery")
		}
		embed = discordgo.MessageEmbed{
			Description: fmt.Sprintf("Image `%d` created!", len(gallery.Images)-1),
			Fields: []*discordgo.MessageEmbedField{
				{
					Name:   "In gallery",
					Value:  fmt.Sprintf("`%s`", galleryName),
					Inline: true,
				},
				{
					Name:   "Added by",
					Value:  fmt.Sprintf("<@%s>", authorId),
					Inline: true,
				},
				{
					Name:   "Created at",
					Value:  fmt.Sprintf("<t:%s>", timestamp),
					Inline: true,
				},
			},
		}
	} else {
		embed = discordgo.MessageEmbed{
			Description: "Gallery does not exist :stop_sign:",
			Color:       0xf04747,
		}
		log.Warn().Interface("interaction", i).Msg("Attempted image retrieval from non-existent gallery")
	}
	data.Embeds = []*discordgo.MessageEmbed{&embed}
	return data
}

func removeImagePrompt(i *discordgo.Interaction) (data discordgo.InteractionResponseData) {
	var embed discordgo.MessageEmbed
	var messageComponents []discordgo.MessageComponent

	command := i.ApplicationCommandData().Options[0]
	galleryName := command.Options[0].StringValue()
	imageNum := int(command.Options[1].IntValue())

	docRef := getGalleryDocRef(galleryName)
	docSnap, err := docRef.Get(ctx)
	if status.Code(err) == codes.NotFound {
		log.Error().Err(err).Caller().Interface("interaction", i).Interface("docRef", docRef).Msg("Attempted to delete image from non-existent gallery")
		embed = discordgo.MessageEmbed{
			Description: "Gallery does not exist :stop_sign:",
			Color:       0xf04747,
		}
		data.Embeds = []*discordgo.MessageEmbed{&embed}
		return data
	}
	var gallery Gallery
	err = docSnap.DataTo(&gallery)
	if err != nil {
		log.Error().Err(err).Caller().Interface("interaction", i).Interface("docSnap", docSnap).Msg("Failed to retrieve document contents")
		embed = discordgo.MessageEmbed{
			Description: "Unable to get gallery contents :stop_sign:",
			Color:       0xf04747,
		}
		data.Embeds = []*discordgo.MessageEmbed{&embed}
		return data
	}
	images := gallery.Images
	numberOfImages := len(images)
	if numberOfImages > 0 {
		if imageNum < 0 || imageNum >= numberOfImages {
			if numberOfImages == 1 {
				embed = discordgo.MessageEmbed{
					Description: "Invalid image number :stop_sign: (Only image number 0 exists.)",
					Color:       0xf04747,
				}
			} else {
				embed = discordgo.MessageEmbed{
					Description: fmt.Sprintf("Invalid image number :stop_sign: (Valid image numbers include 0 through %d inclusive.)", numberOfImages-1),
					Color:       0xf04747,
				}
			}
			data.Embeds = []*discordgo.MessageEmbed{&embed}
			return data
		} else {
			embed = discordgo.MessageEmbed{
				Description: "Are you sure you want to delete the below image? :thinking:",
				Color:       0x5865f2,
				Image: &discordgo.MessageEmbedImage{
					URL: gallery.Images[imageNum]["imageUrl"],
				},
				Fields: []*discordgo.MessageEmbedField{
					{
						Name:   "In gallery",
						Value:  fmt.Sprintf("`%s`", galleryName),
						Inline: true,
					},
					{
						Name:   "Image number",
						Value:  fmt.Sprint(imageNum),
						Inline: true,
					},
					{
						Name:   "Added by",
						Value:  fmt.Sprintf("<@%s>", gallery.Images[imageNum]["authorId"]),
						Inline: true,
					},
					{
						Name:   "Created at",
						Value:  fmt.Sprintf("<t:%s>", gallery.Images[imageNum]["timestamp"]),
						Inline: true,
					},
				},
			}
			messageComponents = []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{
							Label:    "Yes, delete",
							Style:    discordgo.DangerButton,
							CustomID: "image_delete_yes",
						},
						discordgo.Button{
							Label:    "No, cancel",
							Style:    discordgo.SecondaryButton,
							CustomID: "image_delete_no",
						},
					},
				},
			}
			data.Embeds = []*discordgo.MessageEmbed{&embed}
			data.Components = messageComponents
			return data
		}
	} else {
		embed = discordgo.MessageEmbed{
			Description: "Gallery does not exist :stop_sign:",
			Color:       0xf04747,
		}
		log.Warn().Interface("interaction", i).Msg("Attempted image retrieval from non-existent gallery")
		data.Embeds = []*discordgo.MessageEmbed{&embed}
		return data
	}
}

func removeImage(i *discordgo.Interaction, galleryName string, imageNum int) (data discordgo.InteractionResponseData) {
	var embed discordgo.MessageEmbed

	docRef := getGalleryDocRef(galleryName)
	docSnap, err := docRef.Get(ctx)
	if status.Code(err) == codes.NotFound {
		log.Error().Err(err).Caller().Interface("interaction", i).Interface("docRef", docRef).Msg("Attempted to remove image from non-existent gallery")
		embed = discordgo.MessageEmbed{
			Description: "Gallery does not exist :stop_sign:",
			Color:       0xf04747,
		}
		data.Embeds = []*discordgo.MessageEmbed{&embed}
		return data
	}
	var gallery Gallery
	err = docSnap.DataTo(&gallery)
	if err != nil {
		log.Error().Err(err).Caller().Interface("interaction", i).Interface("docSnap", docSnap).Msg("Failed to retrieve document contents")
		embed = discordgo.MessageEmbed{
			Description: "Unable to get gallery contents :stop_sign:",
			Color:       0xf04747,
		}
		data.Embeds = []*discordgo.MessageEmbed{&embed}
		return data
	}
	images := gallery.Images
	numberOfImages := len(images)
	if numberOfImages > 0 {
		if imageNum < 0 || imageNum >= numberOfImages {
			if numberOfImages == 1 {
				embed = discordgo.MessageEmbed{
					Description: "Invalid image number :stop_sign: (Only image number 0 exists.)",
					Color:       0xf04747,
				}
			} else {
				embed = discordgo.MessageEmbed{
					Description: fmt.Sprintf("Invalid image number :stop_sign: (Valid image numbers include 0 through %d inclusive.)", numberOfImages-1),
					Color:       0xf04747,
				}
			}
			data.Embeds = []*discordgo.MessageEmbed{&embed}
			return data
		} else {
			gallery.Images = append(gallery.Images[:imageNum], gallery.Images[imageNum+1:]...)
			_, err = docRef.Set(ctx, gallery)
			if err != nil {
				log.Error().Err(err).Caller().Interface("interaction", i).Interface("DocRef", docRef).Msg("Failed to write document contents")
				embed = discordgo.MessageEmbed{
					Description: "Unable to modify gallery contents :stop_sign:",
					Color:       0xf04747,
				}
				data.Embeds = []*discordgo.MessageEmbed{&embed}
				return data
			} else {
				log.Debug().Str("imageNum", fmt.Sprint(imageNum)).Str("gallery", galleryName).Msg("Image removed from gallery")
			}
			embed = discordgo.MessageEmbed{
				Description: fmt.Sprintf("Image `%d` removed from `%s` :white_check_mark:", imageNum, galleryName),
				Color:       0x43b581,
			}
			data.Embeds = []*discordgo.MessageEmbed{&embed}
			return data
		}
	} else {
		embed = discordgo.MessageEmbed{
			Description: "Gallery does not exist :stop_sign:",
			Color:       0xf04747,
		}
		log.Warn().Interface("interaction", i).Msg("Attempted image removal from non-existent gallery")
		data.Embeds = []*discordgo.MessageEmbed{&embed}
		return data
	}
}

func createGallery(i *discordgo.Interaction) (data discordgo.InteractionResponseData) {
	var embed discordgo.MessageEmbed

	command := i.ApplicationCommandData().Options[0]
	galleryName := command.Options[0].StringValue()

	docRef := getGalleryDocRef(galleryName)
	_, err := docRef.Get(ctx)
	if status.Code(err) == codes.NotFound {
		_, err := docRef.Set(ctx, Gallery{})
		if err != nil {
			log.Error().Err(err).Caller().Interface("interaction", i).Interface("docRef", docRef).Msg("Failed to create document")
			embed = discordgo.MessageEmbed{
				Description: "Unable to create gallery :stop_sign:",
				Color:       0xf04747,
			}
			data.Embeds = []*discordgo.MessageEmbed{&embed}
			return data
		}
		embed = discordgo.MessageEmbed{
			Description: fmt.Sprintf("Gallery `%s` created :white_check_mark:", galleryName),
			Color:       0x43b581,
		}
		log.Debug().Msgf("Created new gallery '%s'", galleryName)
		updateCommands()
	} else if status.Code(err) == codes.OK {
		embed = discordgo.MessageEmbed{
			Description: "Gallery already exists :stop_sign:",
			Color:       0xf04747,
		}
		log.Debug().Msg("Attempted to create a gallery that already exists")
	}
	data.Embeds = []*discordgo.MessageEmbed{&embed}
	return data
}

func deleteGalleryPrompt(i *discordgo.Interaction) (data discordgo.InteractionResponseData) {
	var embed discordgo.MessageEmbed
	var messageComponents []discordgo.MessageComponent

	command := i.ApplicationCommandData().Options[0]
	galleryName := command.Options[0].StringValue()

	docRef := getGalleryDocRef(galleryName)
	_, err := docRef.Get(ctx)
	if status.Code(err) == codes.NotFound {
		log.Error().Err(err).Caller().Interface("interaction", i).Interface("docRef", docRef).Msg("Attempted to delete non-existent gallery")
		embed = discordgo.MessageEmbed{
			Description: "Gallery does not exist :stop_sign:",
			Color:       0xf04747,
		}
		data.Embeds = []*discordgo.MessageEmbed{&embed}
		return data
	}
	embed = discordgo.MessageEmbed{
		Description: "Are you sure you want to delete the following gallery? :thinking:",
		Color:       0x5865f2,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:  "Gallery",
				Value: fmt.Sprintf("`%s`", galleryName),
			},
		},
	}
	messageComponents = []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "Yes, delete",
					Style:    discordgo.DangerButton,
					CustomID: "gallery_delete_yes",
				},
				discordgo.Button{
					Label:    "No, cancel",
					Style:    discordgo.SecondaryButton,
					CustomID: "gallery_delete_no",
				},
			},
		},
	}
	data.Embeds = []*discordgo.MessageEmbed{&embed}
	data.Components = messageComponents
	return data
}

func deleteGallery(i *discordgo.Interaction, galleryName string) (data discordgo.InteractionResponseData) {
	var embed discordgo.MessageEmbed

	docRef := getGalleryDocRef(galleryName)
	_, err := docRef.Get(ctx)
	if status.Code(err) == codes.NotFound {
		log.Error().Err(err).Caller().Interface("interaction", i).Interface("docRef", docRef).Msg("Attempted to delete non-existent gallery")
		embed = discordgo.MessageEmbed{
			Description: "Gallery does not exist :stop_sign:",
			Color:       0xf04747,
		}
		data.Embeds = []*discordgo.MessageEmbed{&embed}
		return data
	}
	_, err = docRef.Delete(ctx)
	if err != nil {
		log.Error().Err(err).Caller().Interface("interaction", i).Interface("docRef", docRef).Msg("Failed to delete document")
		embed = discordgo.MessageEmbed{
			Description: "Unable to delete gallery :stop_sign:",
			Color:       0xf04747,
		}
		data.Embeds = []*discordgo.MessageEmbed{&embed}
		return data
	}
	embed = discordgo.MessageEmbed{
		Description: fmt.Sprintf("Gallery `%s` deleted :white_check_mark:", galleryName),
		Color:       0x43b581,
	}
	data.Embeds = []*discordgo.MessageEmbed{&embed}
	log.Debug().Msgf("Deleted gallery '%s'", galleryName)
	updateCommands()
	return data
}

// Adding/removing galleries has side-effects for the pre-populated galleryName choices
func updateCommands() {
	choices := populateGalleryChoices()
	// Any changes to command order need to be reflected here
	commands[0].Options[0].Options[0].Choices = choices // gallery.random.galleryName.Choices
	commands[0].Options[1].Options[0].Choices = choices // gallery.pick.galleryName.Choices
	commands[0].Options[2].Options[0].Choices = choices // gallery.add_image.galleryName.Choices
	commands[0].Options[3].Options[0].Choices = choices // gallery.remove_image.galleryName.Choices
	commands[0].Options[4].Options[0].Choices = choices // gallery.delete.galleryName.Choices

	for _, v := range commands {
		// log.Debug().Interface("cmd", v).Msg("Attempting to create command")
		_, err := s.ApplicationCommandCreate(s.State.User.ID, config["guildId"], v)
		if err != nil {
			log.Error().Err(err).Caller().Msgf("Cannot (re?)create '%s' command", v.Name)
		} /* else {
			log.Debug().Msgf("Successfully (re)created '%s' command", cmd.Name)
		} */
	}
}

var (
	commands = []*discordgo.ApplicationCommand{
		{
			Name:        "gallery",
			Description: "Server-wide image gallery",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Name:        "random",
					Description: "Send a random image from the chosen gallery",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Name:        "gallery_name",
							Description: "The gallery to choose from",
							Type:        discordgo.ApplicationCommandOptionString,
							Required:    true,
						},
					},
				},
				{
					Name:        "pick",
					Description: "Send the specified image from the chosen gallery",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Name:        "gallery_name",
							Description: "The gallery to choose from",
							Type:        discordgo.ApplicationCommandOptionString,
							Required:    true,
						},
						{
							Name:        "image_number",
							Description: "The image you wish to choose",
							Type:        discordgo.ApplicationCommandOptionInteger,
							Required:    true,
						},
					},
				},
				{
					Name:        "add_image",
					Description: "Add the specified image to the chosen gallery",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Name:        "gallery_name",
							Description: "The gallery to add an image to",
							Type:        discordgo.ApplicationCommandOptionString,
							Required:    true,
						},
						{
							Name:        "image_link",
							Description: "The URL pointing to the image you wish to add",
							Type:        discordgo.ApplicationCommandOptionString,
							Required:    true,
						},
					},
				},
				{
					Name:        "remove_image",
					Description: "Remove the specified image from the chosen gallery",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Name:        "gallery_name",
							Description: "The gallery to remove an image from",
							Type:        discordgo.ApplicationCommandOptionString,
							Required:    true,
						},
						{
							Name:        "image_number",
							Description: "The image you wish to remove",
							Type:        discordgo.ApplicationCommandOptionInteger,
							Required:    true,
						},
					},
				},
				{
					Name:        "delete",
					Description: "Delete an existing gallery",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Name:        "gallery_name",
							Description: "The name of the gallery to be deleted",
							Type:        discordgo.ApplicationCommandOptionString,
							Required:    true,
						},
					},
				},
				{
					Name:        "create",
					Description: "Create a new gallery",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Name:        "gallery_name",
							Description: "The name of the gallery to be created",
							Type:        discordgo.ApplicationCommandOptionString,
							Required:    true,
						},
					},
				},
			},
		},
	}

	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"gallery": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			var data discordgo.InteractionResponseData

			switch i.Type {
			case discordgo.InteractionApplicationCommand:
				command := i.ApplicationCommandData().Options[0]

				switch command.Name {
				case "random":
					data = getRandomImageFromGallery(i.Interaction)
				case "pick":
					data = getImageFromGallery(i.Interaction)
				case "add_image":
					data = addImageToGallery(i.Interaction)
				case "remove_image":
					data = removeImagePrompt(i.Interaction)
				case "create":
					data = createGallery(i.Interaction)
				case "delete":
					data = deleteGalleryPrompt(i.Interaction)
				default:
					embed := discordgo.MessageEmbed{
						Description: "Invalid subcommand :stop_sign:",
						Color:       0xf04747,
					}
					data.Embeds = []*discordgo.MessageEmbed{&embed}
					log.Warn().Interface("interaction", i.Interaction).Msg("Non-existent subcommand invoked")
				}
			default:
				embed := discordgo.MessageEmbed{
					Description: "I didn't expect to be interacted with in this way :flushed:\nPerhaps someone should look into this :thinking:",
					Color:       0xf04747,
				}
				data.Embeds = []*discordgo.MessageEmbed{&embed}
				log.Warn().Interface("interaction", i.Interaction).Msg("Unexpected interaction type")
			}

			err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &data,
			})
			if err != nil {
				log.Error().Err(err).Interface("interaction", i.Interaction).Msg("Failure in responding to interaction")
			}
		},
	}

	componentHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"gallery_delete_yes": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			var data discordgo.InteractionResponseData
			galleryName := i.Message.Embeds[0].Fields[0].Value
			galleryName = strings.Trim(galleryName, "`")
			data = deleteGallery(i.Interaction, galleryName)
			data.Components = []discordgo.MessageComponent{}

			err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseUpdateMessage,
				Data: &data,
			})
			if err != nil {
				log.Error().Err(err).Interface("interaction", i.Interaction).Msg("Failure in responding to interaction")
			}
		},
		"gallery_delete_no": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			galleryName := i.Message.Embeds[0].Fields[0].Value
			galleryName = strings.Trim(galleryName, "`")
			embed := discordgo.MessageEmbed{
				Description: fmt.Sprintf("Cancelled removal of gallery `%s`.", galleryName),
			}

			err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseUpdateMessage,
				Data: &discordgo.InteractionResponseData{
					Embeds:     []*discordgo.MessageEmbed{&embed},
					Components: []discordgo.MessageComponent{},
				},
			})
			if err != nil {
				log.Error().Err(err).Interface("interaction", i.Interaction).Msg("Failure in responding to interaction")
			}
		},
		"image_delete_yes": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			var data discordgo.InteractionResponseData
			galleryName := i.Message.Embeds[0].Fields[0].Value
			galleryName = strings.Trim(galleryName, "`")
			imageNumStr := i.Message.Embeds[0].Fields[1].Value
			imageNum, _ := strconv.Atoi(imageNumStr)

			data = removeImage(i.Interaction, galleryName, imageNum)
			data.Components = []discordgo.MessageComponent{}

			err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseUpdateMessage,
				Data: &data,
			})
			if err != nil {
				log.Error().Err(err).Interface("interaction", i.Interaction).Msg("Failure in responding to interaction")
			}
		},
		"image_delete_no": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			galleryName := i.Message.Embeds[0].Fields[0].Value
			galleryName = strings.Trim(galleryName, "`")
			imageNum := i.Message.Embeds[0].Fields[1].Value
			embed := discordgo.MessageEmbed{
				Description: fmt.Sprintf("Cancelled removal of image `%s` from gallery `%s`.", imageNum, galleryName),
			}

			err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseUpdateMessage,
				Data: &discordgo.InteractionResponseData{
					Embeds:     []*discordgo.MessageEmbed{&embed},
					Components: []discordgo.MessageComponent{},
				},
			})
			if err != nil {
				log.Error().Err(err).Interface("interaction", i.Interaction).Msg("Failure in responding to interaction")
			}
		},
	}
)

func main() {
	var err error

	firestoreClient, err = firestore.NewClient(ctx, config["projectId"], option.WithCredentialsFile(config["googleApplicationCredentialsPath"]))
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create Firestore client")
	}
	defer firestoreClient.Close()

	s, err = discordgo.New("Bot " + config["botToken"])
	if err != nil {
		log.Fatal().Err(err).Msg("Invalid bot parameters")
	}

	s.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			if h, ok := commandHandlers[i.ApplicationCommandData().Name]; ok {
				h(s, i)
			}
		case discordgo.InteractionMessageComponent:
			if h, ok := componentHandlers[i.MessageComponentData().CustomID]; ok {
				h(s, i)
			}
		}
	})

	s.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		log.Info().Msg("Bot is up!")
	})
	err = s.Open()
	if err != nil {
		log.Fatal().Err(err).Msg("Cannot open the session")
	}

	defer s.Close()

	updateCommands()

	stop := make(chan os.Signal)
	signal.Notify(stop, os.Interrupt)
	<-stop
	log.Info().Msg("Exiting gracefully")
}
