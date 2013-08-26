package mpdfav

import (
	"log"
	"strconv"
	"time"
)

const (
	songPlayedThresholdSeconds = 10
	tickMillis                 = 900
	playcountSticker           = "playcount"
)

type songStatusInfo struct {
	StatusInfo Info
	SongInfo   Info
}

func incSongPlayCount(songInfo *Info, mpdc *MPDClient) (int, error) {
	value, err := mpdc.StickerGet(
		StickerSongType,
		(*songInfo)["file"],
		playcountSticker,
	)
	if err != nil {
		return -1, err
	}
	if len(value) == 0 {
		value = "0"
	}
	intval, err := strconv.Atoi(value)
	if err != nil {
		return -1, err
	}
	intval += 1
	err = mpdc.StickerSet(
		StickerSongType,
		(*songInfo)["file"],
		playcountSticker,
		strconv.Itoa(intval),
	)
	return intval, err
}

func considerSongPlayed(statusInfo *Info, limit int) bool {
	current, total := statusInfo.Progress()
	if total == 0 || current == 0 {
		return false
	}
	return (total - current) < limit
}

func checkSongChange(si *songStatusInfo, mpdc *MPDClient) error {
	info, err := mpdc.Status()
	if err != nil {
		return err
	}

	if info["songid"] != si.StatusInfo["songid"] {
		played := considerSongPlayed(&si.StatusInfo, songPlayedThresholdSeconds)

		if played {
			playcount, err := incSongPlayCount(&si.SongInfo, mpdc)
			if err != nil {
				return err
			}
			log.Println("Playcounts:", si.SongInfo["Title"], " playcount=", playcount)
		}
	}
	si.StatusInfo = info
	return nil
}

func updateSongInfo(si *songStatusInfo, mpdc *MPDClient) error {
	songInfo, err := mpdc.CurrentSong()
	if err != nil {
		return err
	}
	si.SongInfo = songInfo
	return nil
}

func processStateUpdate(si *songStatusInfo, mpdc *MPDClient) error {
	err := checkSongChange(si, mpdc)
	if err != nil {
		return err
	}
	// We store the current song after processing,
	// since that should be the next song playing already.
	err = updateSongInfo(si, mpdc)
	if err != nil {
		return err
	}
	return nil
}

func RecordPlayCounts(mpdc *MPDClient) {
	mpdcIdle, err := ConnectDup(mpdc)
	defer mpdcIdle.Close()
	if err != nil {
		panic(err)
	}
	statusInfo, err := mpdc.Status()
	if err != nil {
		panic(err)
	}
	songInfo, err := mpdc.CurrentSong()
	if err != nil {
		panic(err)
	}

	si := songStatusInfo{}
	si.StatusInfo = statusInfo
	si.SongInfo = songInfo

	pollCh := time.Tick(tickMillis * time.Millisecond)
	idleCh := make(chan string)
	ignorePoll := si.StatusInfo["state"] != "play"

	go func() {
		for {
			subsystem, err := mpdcIdle.Idle("player")
			if err != nil {
				panic(err)
			} else {
				idleCh <- subsystem
			}
		}
	}()

	for {
		select {
		case <-pollCh:
			if !ignorePoll {
				err = processStateUpdate(&si, mpdc)
				if err != nil {
					panic(err)
				}
			}
		case <-idleCh:
			err := processStateUpdate(&si, mpdc)
			if err != nil {
				panic(err)
			}

			// Suspend poll goroutine if player is stopped or paused
			ignorePoll = si.StatusInfo["state"] != "play"
		}
	}
}
