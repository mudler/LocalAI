package utils

import "sync"

// This function is intended to be used in the situation where one has a slice of channels that will each eventually publish a single vue and then be closed,
// 		and one needs to emit a single slice value on another channel that collapses the results down
// Specifically, this is the most basic variant without error handling
func SliceOfChannelsResultSynchronizer[IR any, FR any](individualResultChannels []<-chan IR,
	finalResultChannel chan<- []FR, mappingFn func(IR) FR) {

	var wg sync.WaitGroup
	wg.Add(len(individualResultChannels))
	resultMutex := sync.Mutex{}
	result := []FR{}
	for _, irc := range individualResultChannels {
		go func(c <-chan IR) {
			defer wg.Done()
			ir := <-c
			resultMutex.Lock()
			result = append(result, mappingFn(ir))
			resultMutex.Unlock()
		}(irc)
	}
	wg.Wait()
	finalResultChannel <- result
	close(finalResultChannel)
}

// This function is intended to be used in the situation where one has a slice of channels that will each eventually publish a single vue and then be closed,
// 		and one needs to emit a single slice value on another channel that collapses the results down
// This version assumes any error is fatal - Any Error result on an IRC or any error during mapping will result in an Error value on the final result channel
// TODO: Should this duplicate code, or be merged with above?
// TODO NEEDS WORK WAIT MISSING????
func SliceOfChannelsResultSynchronizerFatalErrors[IR any, FR any](individualResultChannels []<-chan ErrorOr[IR],
	finalResultChannel chan<- ErrorOr[[]FR], mappingFn func(IR) (FR, error)) {

	var wg sync.WaitGroup
	wg.Add(len(individualResultChannels))
	resultMutex := sync.Mutex{}
	result := []FR{}
	var err error
	for _, irc := range individualResultChannels {
		go func(c <-chan ErrorOr[IR]) {
			defer wg.Done()
			ir := <-c
			resultMutex.Lock()
			if ir.Error != nil {
				err = ir.Error
			} else {
				mVal, mErr := mappingFn(ir.Value)
				if mErr != nil {
					err = mErr
				} else {
					result = append(result, mVal)
				}
			}
			resultMutex.Unlock()
		}(irc)
	}
	wg.Wait()
	if err != nil {
		finalResultChannel <- ErrorOr[[]FR]{Error: err}
	}
	finalResultChannel <- ErrorOr[[]FR]{Value: result}
	close(finalResultChannel)
}

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
