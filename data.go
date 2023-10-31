package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip10"
	"github.com/nbd-wtf/go-nostr/nip19"
)

type EnhancedEvent struct {
	event  *nostr.Event
	relays []string
}

func (ee EnhancedEvent) IsReply() bool {
	return nip10.GetImmediateReply(ee.event.Tags) != nil
}

func (ee EnhancedEvent) Preview() template.HTML {
	lines := strings.Split(html.EscapeString(ee.event.Content), "\n")
	var processedLines []string
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		processedLine := shortenNostrURLs(line)
		processedLines = append(processedLines, processedLine)
	}

	return template.HTML(strings.Join(processedLines, "<br/>"))
}

func (ee EnhancedEvent) Npub() string {
	npub, _ := nip19.EncodePublicKey(ee.event.PubKey)
	return npub
}

func (ee EnhancedEvent) NpubShort() string {
	npub := ee.Npub()
	return npub[:8] + "…" + npub[len(npub)-4:]
}

func (ee EnhancedEvent) Nevent() string {
	nevent, _ := nip19.EncodeEvent(ee.event.ID, ee.relays, ee.event.PubKey)
	return nevent
}

func (ee EnhancedEvent) CreatedAtStr() string {
	return time.Unix(int64(ee.event.CreatedAt), 0).Format("2006-01-02 15:04:05")
}

func (ee EnhancedEvent) ModifiedAtStr() string {
	return time.Unix(int64(ee.event.CreatedAt), 0).Format("2006-01-02T15:04:05Z07:00")
}

type Data struct {
	templateId          TemplateID
	event               *nostr.Event
	relays              []string
	npub                string
	npubShort           string
	nprofile            string
	nevent              string
	neventNaked         string
	naddr               string
	naddrNaked          string
	createdAt           string
	modifiedAt          string
	parentLink          template.HTML
	metadata            nostr.ProfileMetadata
	authorRelays        []string
	authorLong          string
	authorShort         string
	renderableLastNotes []EnhancedEvent
	kindDescription     string
	kindNIP             string
	video               string
	videoType           string
	image               string
	content             string
	alt                 string
	kind1063Metadata    *Kind1063Metadata
}

type Kind1063Metadata struct {
	Magnet    string
	Dim       string
	Size      string
	Summary   string
	Image     string
	URL       string
	AES256GCM string
	M         string
	X         string
	I         string
	Blurhash  string
	Thumb     string
}

func (fm Kind1063Metadata) IsVideo() bool { return strings.Split(fm.M, "/")[0] == "video" }
func (fm Kind1063Metadata) IsImage() bool { return strings.Split(fm.M, "/")[0] == "image" }
func (fm Kind1063Metadata) DisplayImage() string {
	if fm.Image != "" {
		return fm.Image
	} else if fm.IsImage() {
		return fm.URL
	} else {
		return ""
	}
}

func grabData(ctx context.Context, code string, isProfileSitemap bool) (*Data, error) {
	// code can be a nevent, nprofile, npub or nip05 identifier, in which case we try to fetch the associated event
	event, relays, err := getEvent(ctx, code, nil)
	if err != nil {
		log.Warn().Err(err).Str("code", code).Msg("failed to fetch event for code")
		return nil, err
	}

	relaysForNip19 := make([]string, 0, 3)
	for i, relay := range relays {
		relaysForNip19 = append(relaysForNip19, relay)
		if i == 2 {
			break
		}
	}

	data := &Data{
		event: event,
	}

	data.npub, _ = nip19.EncodePublicKey(event.PubKey)
	data.nevent, _ = nip19.EncodeEvent(event.ID, relaysForNip19, event.PubKey)
	data.neventNaked, _ = nip19.EncodeEvent(event.ID, nil, event.PubKey)
	data.naddr = ""
	data.naddrNaked = ""
	data.createdAt = time.Unix(int64(event.CreatedAt), 0).Format("2006-01-02 15:04:05")
	data.modifiedAt = time.Unix(int64(event.CreatedAt), 0).Format("2006-01-02T15:04:05Z07:00")

	author := event
	data.authorRelays = []string{}

	eventRelays := []string{}
	for _, relay := range relays {
		for _, excluded := range excludedRelays {
			if strings.Contains(relay, excluded) {
				continue
			}
		}
		if strings.Contains(relay, "/npub1") {
			continue // skip relays with personalyzed query like filter.nostr.wine
		}
		eventRelays = append(eventRelays, trimProtocol(relay))
	}

	if tag := event.Tags.GetFirst([]string{"alt", ""}); tag != nil {
		data.alt = (*tag)[1]
	}

	switch event.Kind {
	case 0:
		{
			rawAuthorRelays := []string{}
			ctx, cancel := context.WithTimeout(ctx, time.Second*4)
			rawAuthorRelays = relaysForPubkey(ctx, event.PubKey)
			cancel()
			for _, relay := range rawAuthorRelays {
				for _, excluded := range excludedRelays {
					if strings.Contains(relay, excluded) {
						continue
					}
				}
				if strings.Contains(relay, "/npub1") {
					continue // skip relays with personalyzed query like filter.nostr.wine
				}
				data.authorRelays = append(data.authorRelays, trimProtocol(relay))
			}
		}

		lastNotes := authorLastNotes(ctx, event.PubKey, data.authorRelays, isProfileSitemap)
		data.renderableLastNotes = make([]EnhancedEvent, len(lastNotes))
		for i, levt := range lastNotes {
			data.renderableLastNotes[i] = EnhancedEvent{levt, []string{}}
		}
		if err != nil {
			return nil, err
		}
	case 1, 7, 30023, 30024:
		data.templateId = Note
		data.content = event.Content
		if parentNevent := getParentNevent(event); parentNevent != "" {
			data.parentLink = template.HTML(replaceNostrURLsWithTags(nostrNoteNeventMatcher, "nostr:"+parentNevent))
		}
	case 6:
		data.templateId = Note
		if reposted := event.Tags.GetFirst([]string{"e", ""}); reposted != nil {
			original_nevent, _ := nip19.EncodeEvent((*reposted)[1], []string{}, "")
			data.content = "Repost of nostr:" + original_nevent
		}
	case 1063:
		data.templateId = FileMetadata
		data.kind1063Metadata = &Kind1063Metadata{}

		if tag := event.Tags.GetFirst([]string{"url", ""}); tag != nil {
			data.kind1063Metadata.URL = (*tag)[1]
		}
		if tag := event.Tags.GetFirst([]string{"m", ""}); tag != nil {
			data.kind1063Metadata.M = (*tag)[1]
		}
		if tag := event.Tags.GetFirst([]string{"aes-256-gcm", ""}); tag != nil {
			data.kind1063Metadata.AES256GCM = (*tag)[1]
		}
		if tag := event.Tags.GetFirst([]string{"x", ""}); tag != nil {
			data.kind1063Metadata.X = (*tag)[1]
		}
		if tag := event.Tags.GetFirst([]string{"size", ""}); tag != nil {
			data.kind1063Metadata.Size = (*tag)[1]
		}
		if tag := event.Tags.GetFirst([]string{"dim", ""}); tag != nil {
			data.kind1063Metadata.Dim = (*tag)[1]
		}
		if tag := event.Tags.GetFirst([]string{"magnet", ""}); tag != nil {
			data.kind1063Metadata.Magnet = (*tag)[1]
		}
		if tag := event.Tags.GetFirst([]string{"i", ""}); tag != nil {
			data.kind1063Metadata.I = (*tag)[1]
		}
		if tag := event.Tags.GetFirst([]string{"blurhash", ""}); tag != nil {
			data.kind1063Metadata.Blurhash = (*tag)[1]
		}
		if tag := event.Tags.GetFirst([]string{"thumb", ""}); tag != nil {
			data.kind1063Metadata.Thumb = (*tag)[1]
		}
		if tag := event.Tags.GetFirst([]string{"image", ""}); tag != nil {
			data.kind1063Metadata.Image = (*tag)[1]
		}
		if tag := event.Tags.GetFirst([]string{"summary", ""}); tag != nil {
			data.kind1063Metadata.Summary = (*tag)[1]
		}
	default:
		if event.Kind >= 30000 && event.Kind < 40000 {
			data.templateId = Other
			if d := event.Tags.GetFirst([]string{"d", ""}); d != nil {
				data.naddr, _ = nip19.EncodeEntity(event.PubKey, event.Kind, d.Value(), relaysForNip19)
				data.naddrNaked, _ = nip19.EncodeEntity(event.PubKey, event.Kind, d.Value(), nil)
			}
		}
	}

	if event.Kind != 0 {
		ctx, cancel := context.WithTimeout(ctx, time.Second*3)
		author, relays, _ = getEvent(ctx, data.npub, relaysForNip19)
		if len(relays) > 0 {
			data.nprofile, _ = nip19.EncodeProfile(event.PubKey, limitAt(relays, 2))
		}
		cancel()
	}

	data.kindDescription = kindNames[event.Kind]
	if data.kindDescription == "" {
		data.kindDescription = fmt.Sprintf("Kind %d", event.Kind)
	}
	data.kindNIP = kindNIPs[event.Kind]

	if event.Kind == 1063 {
		if data.kind1063Metadata.IsImage() {
			data.image = data.kind1063Metadata.URL
		} else if data.kind1063Metadata.IsVideo() {
			data.video = data.kind1063Metadata.URL
			data.videoType = strings.Split(data.kind1063Metadata.M, "/")[1]
		}
	} else {
		urls := urlMatcher.FindAllString(event.Content, -1)
		for _, url := range urls {
			switch {
			case imageExtensionMatcher.MatchString(url):
				if data.image == "" {
					data.image = url
				}
			case videoExtensionMatcher.MatchString(url):
				if data.video == "" {
					data.video = url
					if strings.HasSuffix(data.video, "mp4") {
						data.videoType = "mp4"
					} else if strings.HasSuffix(data.video, "mov") {
						data.videoType = "mov"
					} else {
						data.videoType = "webm"
					}
				}
			}
		}
	}

	data.npubShort = data.npub[:8] + "…" + data.npub[len(data.npub)-4:]
	data.authorLong = data.npub
	data.authorShort = data.npubShort

	var metadata nostr.ProfileMetadata
	if author != nil {
		if err := json.Unmarshal([]byte(author.Content), &metadata); err == nil {
			data.authorLong = fmt.Sprintf("%s (%s)", metadata.Name, data.npub)
			data.authorShort = fmt.Sprintf("%s (%s)", metadata.Name, data.npubShort)
		}
	}

	return data, nil
}
