package sound

/*

MIT License

Copyright (c) 2024 Xbozon

*/

func ResampleInt16(input []int16, inputRate, outputRate int) []int16 {
	// Calculate the resampling ratio
	ratio := float64(inputRate) / float64(outputRate)

	// Calculate the length of the resampled output
	outputLength := int(float64(len(input)) / ratio)

	// Allocate a slice for the resampled output
	output := make([]int16, outputLength)

	// Perform linear interpolation for resampling
	for i := 0; i < outputLength-1; i++ {
		// Calculate the corresponding position in the input
		pos := float64(i) * ratio

		// Calculate the indices of the surrounding input samples
		indexBefore := int(pos)
		indexAfter := indexBefore + 1
		if indexAfter >= len(input) {
			indexAfter = len(input) - 1
		}

		// Calculate the fractional part of the position
		frac := pos - float64(indexBefore)

		// Linearly interpolate between the two surrounding input samples
		output[i] = int16((1-frac)*float64(input[indexBefore]) + frac*float64(input[indexAfter]))
	}

	// Handle the last sample explicitly to avoid index out of range
	output[outputLength-1] = input[len(input)-1]

	return output
}

func ConvertInt16ToInt(input []int16) []int {
	output := make([]int, len(input)) // Allocate a slice for the output
	for i, value := range input {
		output[i] = int(value) // Convert each int16 to int and assign it to the output slice
	}
	return output // Return the converted slice
}

func BytesToInt16sLE(bytes []byte) []int16 {
	// Ensure the byte slice length is even
	if len(bytes)%2 != 0 {
		panic("bytesToInt16sLE: input bytes slice has odd length, must be even")
	}

	int16s := make([]int16, len(bytes)/2)
	for i := 0; i < len(int16s); i++ {
		int16s[i] = int16(bytes[2*i]) | int16(bytes[2*i+1])<<8
	}
	return int16s
}
