package utils

import (
	"sync"

	"github.com/rs/zerolog/log"
)

// TODO: closeWhenDone bool parameter ::
//			It currently is experimental, and therefore exists.
//			Is there ever a situation to use false?

func SliceOfChannelsRawMerger[IR any, MR any](individualResultChannels []<-chan IR, outputChannel chan<- MR, mappingFn func(IR) (MR, error), closeWhenDone bool) *sync.WaitGroup {
	var wg sync.WaitGroup
	wg.Add(len(individualResultChannels))
	for _, irc := range individualResultChannels {
		go func(c <-chan IR) {
			for r := range c {
				mr, err := mappingFn(r)
				if err == nil {
					outputChannel <- mr
				}
			}
			wg.Done()
		}(irc)
	}
	if closeWhenDone {
		go func() {
			wg.Wait()
			close(outputChannel)
		}()
	}

	return &wg
}

func SliceOfChannelsRawMergerWithoutMapping[T any](individualResultsChannels []<-chan T, outputChannel chan<- T, closeWhenDone bool) *sync.WaitGroup {
	return SliceOfChannelsRawMerger(individualResultsChannels, outputChannel, func(v T) (T, error) { return v, nil }, closeWhenDone)
}

// TODO: now that above mapper is fixed, commonize with above???
func SliceOfChannelsMergerWithErrors[IV any, OV any](individualResultChannels []<-chan ErrorOr[IV], successChannel chan<- OV, errorChannel chan<- error, mappingFn func(IV) (OV, error), closeWhenDone bool) *sync.WaitGroup {
	var wg sync.WaitGroup
	wg.Add(len(individualResultChannels))
	for _, irc := range individualResultChannels {
		go func(c <-chan ErrorOr[IV]) {
			for r := range c {
				if r.Error != nil {
					errorChannel <- r.Error
				} else {
					mv, err := mappingFn(r.Value)
					if err != nil {
						errorChannel <- err
					} else {
						successChannel <- mv
					}
				}
			}
			wg.Done()
		}(irc)
	}
	if closeWhenDone {
		go func() {
			wg.Wait()
			close(successChannel)
			close(errorChannel)
		}()
	}
	return &wg
}

// TODO: This seems like a hack for now. Revist post port?
// TODO: It's also no longer used... Delete this?
func SliceOfChannelsMergerIgnoreErrors[T any](individualResultsChannels []<-chan ErrorOr[*T], outputChannel chan<- *T, closeWhenDone bool) *sync.WaitGroup {
	return SliceOfChannelsRawMerger(individualResultsChannels, outputChannel, func(v ErrorOr[*T]) (*T, error) {
		if v.Error != nil {
			// TEMPORARY DEBUG LOG LINE GOES HERE LATER

			return nil, v.Error
		}
		return v.Value, nil
	}, closeWhenDone)
}

func SliceOfChannelsReducer[IV any, OV any](individualResultsChannels []<-chan IV, outputChannel chan<- OV,
	reducerFn func(iv IV, ov OV) OV, initialValue OV, closeWhenDone bool) (wg *sync.WaitGroup) {
	wg = &sync.WaitGroup{}
	wg.Add(len(individualResultsChannels))
	reduceLock := sync.Mutex{}
	for _, irc := range individualResultsChannels {
		go func(c <-chan IV) {
			for iv := range c {
				reduceLock.Lock()
				initialValue = reducerFn(iv, initialValue)
				reduceLock.Unlock()
			}
			wg.Done()
		}(irc)
	}
	go func() {
		log.Debug().Msgf("==================== DELTEME SliceOfChannelsReducer Goroutine TOP %d", len(individualResultsChannels))
		wg.Wait()
		log.Debug().Msgf("==================== DELTEME SliceOfChannelsReducer Goroutine WAIT DONE CLOSE? %t", closeWhenDone)
		outputChannel <- initialValue
		if closeWhenDone {
			close(outputChannel)
			log.Debug().Msg("==================== DELTEME SliceOfChannelsReducer output channel CLOSED!!!")
		}
	}()
	return wg
}
