package utils

import "sync"

func SliceOfChannelsRawMerger[IR any, MR any](individualResultChannels []<-chan IR, outputChannel chan<- MR, mappingFn func(IR) (MR, error)) *sync.WaitGroup {
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
	return &wg
}

func SliceOfChannelsRawMergerWithoutMapping[T any](individualResultsChannels []<-chan T, outputChannel chan<- T) *sync.WaitGroup {
	return SliceOfChannelsRawMerger(individualResultsChannels, outputChannel, func(v T) (T, error) { return v, nil })
}

// TODO: now that above mapper is fixed, commonize with above???
func SliceOfChannelsMergerWithErrors[IV any, OV any](individualResultChannels []<-chan ErrorOr[IV], successChannel chan<- OV, errorChannel chan<- error, mappingFn func(IV) (OV, error)) *sync.WaitGroup {
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
	return &wg
}

// TODO: This seems like a hack for now. Revist post port?
func SliceOfChannelsMergerIgnoreErrors[T any](individualResultsChannels []<-chan ErrorOr[*T], outputChannel chan<- *T) *sync.WaitGroup {
	return SliceOfChannelsRawMerger(individualResultsChannels, outputChannel, func(v ErrorOr[*T]) (*T, error) {
		if v.Error != nil {
			// TEMPORARY DEBUG LOG LINE GOES HERE LATER

			return nil, v.Error
		}
		return v.Value, nil
	})
}

func SliceOfChannelsReducer[IV any, OV any](individualResultsChannels []<-chan IV, outputChannel chan<- OV,
	reducerFn func(iv IV, ov OV) OV, initialValue OV) (wg *sync.WaitGroup) {
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
		wg.Wait()
		outputChannel <- initialValue
	}()
	return wg
}
