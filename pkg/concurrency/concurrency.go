package concurrency

import (
	"sync"
)

// TODO: closeWhenDone bool parameter ::
//			It currently is experimental, and therefore exists.
//			Is there ever a situation to use false?

// This function is used to merge the results of a slice of channels of a specific result type down to a single result channel of a second type.
// mappingFn allows the caller to convert from the input type to the output type
// if closeWhenDone is set to true, the output channel will be closed when all individual result channels of the slice have been closed - otherwise it will be left open for future use.
// The same WaitGroup used to trigger that optional closing is returned for any other synchronization purposes.
func SliceOfChannelsRawMerger[IndividualResultType any, OutputResultType any](individualResultChannels []<-chan IndividualResultType, outputChannel chan<- OutputResultType, mappingFn func(IndividualResultType) (OutputResultType, error), closeWhenDone bool) *sync.WaitGroup {
	var wg sync.WaitGroup
	wg.Add(len(individualResultChannels))
	mergingFn := func(c <-chan IndividualResultType) {
		for r := range c {
			mr, err := mappingFn(r)
			if err == nil {
				outputChannel <- mr
			}
		}
		wg.Done()
	}
	for _, irc := range individualResultChannels {
		go mergingFn(irc)
	}
	if closeWhenDone {
		go func() {
			wg.Wait()
			close(outputChannel)
		}()
	}

	return &wg
}

// This function is used to merge the results of a slice of channels of a specific result type down to a single result channel of THE SAME TYPE.
// if closeWhenDone is set to true, the output channel will be closed when all individual result channels of the slice have been closed - otherwise it will be left open for future use.
// The same WaitGroup used to trigger that optional closing is returned for any other synchronization purposes.
func SliceOfChannelsRawMergerWithoutMapping[ResultType any](individualResultsChannels []<-chan ResultType, outputChannel chan<- ResultType, closeWhenDone bool) *sync.WaitGroup {
	return SliceOfChannelsRawMerger(individualResultsChannels, outputChannel, func(v ResultType) (ResultType, error) { return v, nil }, closeWhenDone)
}

// This function is used to merge the results of a slice of channels of a specific result type down to a single succcess result channel of a second type, and an error channel
// mappingFn allows the caller to convert from the input type to the output type
// This variant is designed to be aware of concurrency.ErrorOr[T], splitting successes from failures.
// if closeWhenDone is set to true, the output channel will be closed when all individual result channels of the slice have been closed - otherwise it will be left open for future use.
// The same WaitGroup used to trigger that optional closing is returned for any other synchronization purposes.
func SliceOfChannelsMergerWithErrors[IndividualResultType any, OutputResultType any](individualResultChannels []<-chan ErrorOr[IndividualResultType], successChannel chan<- OutputResultType, errorChannel chan<- error, mappingFn func(IndividualResultType) (OutputResultType, error), closeWhenDone bool) *sync.WaitGroup {
	var wg sync.WaitGroup
	wg.Add(len(individualResultChannels))
	mergingFn := func(c <-chan ErrorOr[IndividualResultType]) {
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
	}
	for _, irc := range individualResultChannels {
		go mergingFn(irc)
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

// This function is used to reduce down the results of a slice of channels of a specific result type down to a single result value of a second type.
// reducerFn allows the caller to convert from the input type to the output type
// if closeWhenDone is set to true, the output channel will be closed when all individual result channels of the slice have been closed - otherwise it will be left open for future use.
// The same WaitGroup used to trigger that optional closing is returned for any other synchronization purposes.
func SliceOfChannelsReducer[InputResultType any, OutputResultType any](individualResultsChannels []<-chan InputResultType, outputChannel chan<- OutputResultType,
	reducerFn func(iv InputResultType, ov OutputResultType) OutputResultType, initialValue OutputResultType, closeWhenDone bool) (wg *sync.WaitGroup) {
	wg = &sync.WaitGroup{}
	wg.Add(len(individualResultsChannels))
	reduceLock := sync.Mutex{}
	reducingFn := func(c <-chan InputResultType) {
		for iv := range c {
			reduceLock.Lock()
			initialValue = reducerFn(iv, initialValue)
			reduceLock.Unlock()
		}
		wg.Done()
	}
	for _, irc := range individualResultsChannels {
		go reducingFn(irc)
	}
	go func() {
		wg.Wait()
		outputChannel <- initialValue
		if closeWhenDone {
			close(outputChannel)
		}
	}()
	return wg
}

// This function is primarily designed to be used in combination with the above utility functions.
// A slice of input result channels of a specific type is provided, along with a function to map those values to another type
// A slice of output result channels is returned, where each value is mapped as it comes in.
// The order of the slice will be retained.
func SliceOfChannelsTransformer[InputResultType any, OutputResultType any](inputChanels []<-chan InputResultType, mappingFn func(v InputResultType) OutputResultType) (outputChannels []<-chan OutputResultType) {
	rawOutputChannels := make([]<-chan OutputResultType, len(inputChanels))

	transformingFn := func(ic <-chan InputResultType, oc chan OutputResultType) {
		for iv := range ic {
			oc <- mappingFn(iv)
		}
		close(oc)
	}

	for ci, c := range inputChanels {
		roc := make(chan OutputResultType)
		go transformingFn(c, roc)
		rawOutputChannels[ci] = roc
	}

	outputChannels = rawOutputChannels
	return
}
