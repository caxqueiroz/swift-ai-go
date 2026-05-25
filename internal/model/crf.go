package model

import (
	"fmt"
	"math"
)

type CRF struct {
	Start             []float64
	End               []float64
	Transitions       [][]float64
	TransitionsOrder2 [][]float64
}

func (c CRF) Decode(emissions [][][]float64, mask [][]bool) [][]int {
	validateCRFInputs(emissions, mask)

	paths := make([][]int, len(emissions))
	for batchIdx, batch := range emissions {
		positions := validPositions(len(batch), maskForBatch(mask, batchIdx))
		if len(positions) == 0 {
			paths[batchIdx] = []int{}
			continue
		}

		tagCount := len(batch[positions[0]])
		scores := make([]float64, tagCount)
		for tag := 0; tag < tagCount; tag++ {
			scores[tag] = valueAt(c.Start, tag) + batch[positions[0]][tag]
		}

		backpointers := make([][]int, len(positions)-1)
		for t := 1; t < len(positions); t++ {
			nextScores := make([]float64, tagCount)
			backpointers[t-1] = make([]int, tagCount)
			for next := 0; next < tagCount; next++ {
				bestScore := math.Inf(-1)
				bestTag := 0
				for prev := 0; prev < tagCount; prev++ {
					score := scores[prev] + c.transition(prev, next) + batch[positions[t]][next]
					if score > bestScore {
						bestScore = score
						bestTag = prev
					}
				}
				nextScores[next] = bestScore
				backpointers[t-1][next] = bestTag
			}
			scores = nextScores
		}

		bestScore := math.Inf(-1)
		bestTag := 0
		for tag := 0; tag < tagCount; tag++ {
			score := scores[tag] + valueAt(c.End, tag)
			if score > bestScore {
				bestScore = score
				bestTag = tag
			}
		}

		path := make([]int, len(positions))
		path[len(path)-1] = bestTag
		for t := len(backpointers) - 1; t >= 0; t-- {
			path[t] = backpointers[t][path[t+1]]
		}
		paths[batchIdx] = path
	}
	return paths
}

func (c CRF) MarginalProbabilities(emissions [][][]float64, mask [][]bool) [][][]float64 {
	seqLen, tagCount := validateCRFInputs(emissions, mask)
	batchLen := len(emissions)
	probs := make([][][]float64, seqLen)
	for seq := range probs {
		probs[seq] = make([][]float64, batchLen)
		for batch := range probs[seq] {
			probs[seq][batch] = make([]float64, tagCount)
		}
	}

	for batchIdx, batch := range emissions {
		positions := validPositions(len(batch), maskForBatch(mask, batchIdx))
		if len(positions) == 0 {
			continue
		}

		localTagCount := len(batch[positions[0]])
		alpha := make([][]float64, len(positions))
		alpha[0] = make([]float64, localTagCount)
		for tag := 0; tag < localTagCount; tag++ {
			alpha[0][tag] = valueAt(c.Start, tag) + batch[positions[0]][tag]
		}
		for t := 1; t < len(positions); t++ {
			alpha[t] = make([]float64, localTagCount)
			for next := 0; next < localTagCount; next++ {
				scores := make([]float64, localTagCount)
				for prev := 0; prev < localTagCount; prev++ {
					scores[prev] = alpha[t-1][prev] + c.transition(prev, next)
				}
				alpha[t][next] = batch[positions[t]][next] + logSumExp(scores)
			}
		}

		beta := make([][]float64, len(positions))
		beta[len(positions)-1] = make([]float64, localTagCount)
		for tag := 0; tag < localTagCount; tag++ {
			beta[len(positions)-1][tag] = valueAt(c.End, tag)
		}
		for t := len(positions) - 2; t >= 0; t-- {
			beta[t] = make([]float64, localTagCount)
			for prev := 0; prev < localTagCount; prev++ {
				scores := make([]float64, localTagCount)
				for next := 0; next < localTagCount; next++ {
					scores[next] = c.transition(prev, next) + batch[positions[t+1]][next] + beta[t+1][next]
				}
				beta[t][prev] = logSumExp(scores)
			}
		}

		finalScores := make([]float64, localTagCount)
		for tag := 0; tag < localTagCount; tag++ {
			finalScores[tag] = alpha[len(positions)-1][tag] + valueAt(c.End, tag)
		}
		z := logSumExp(finalScores)
		if math.IsInf(z, -1) {
			continue
		}

		for t, seq := range positions {
			for tag := 0; tag < localTagCount; tag++ {
				probs[seq][batchIdx][tag] = math.Exp(alpha[t][tag] + beta[t][tag] - z)
			}
		}
	}

	return probs
}

func (c CRF) transition(from, to int) float64 {
	score := matrixValueAt(c.Transitions, from, to)
	if len(c.TransitionsOrder2) > 0 {
		score += matrixValueAt(c.TransitionsOrder2, from, to)
	}
	return score
}

func validateCRFInputs(emissions [][][]float64, mask [][]bool) (int, int) {
	if mask != nil && len(mask) != len(emissions) {
		panic(fmt.Sprintf("mask batch size %d does not match emissions batch size %d", len(mask), len(emissions)))
	}

	seqLen := 0
	tagCount := 0
	for batchIdx, batch := range emissions {
		if len(batch) > seqLen {
			seqLen = len(batch)
		}
		if mask != nil && len(mask[batchIdx]) != len(batch) {
			panic(fmt.Sprintf("mask[%d] length %d does not match emissions[%d] sequence length %d", batchIdx, len(mask[batchIdx]), batchIdx, len(batch)))
		}
		for seqIdx, token := range batch {
			if len(token) == 0 {
				panic(fmt.Sprintf("emissions[%d][%d] has zero tags", batchIdx, seqIdx))
			}
			if tagCount == 0 {
				tagCount = len(token)
				continue
			}
			if len(token) != tagCount {
				panic(fmt.Sprintf("emissions[%d][%d] tag count %d does not match tag count %d", batchIdx, seqIdx, len(token), tagCount))
			}
		}
	}
	return seqLen, tagCount
}

func logSumExp(values []float64) float64 {
	if len(values) == 0 {
		return math.Inf(-1)
	}

	maxValue := values[0]
	for _, value := range values[1:] {
		if value > maxValue {
			maxValue = value
		}
	}
	if math.IsInf(maxValue, -1) {
		return maxValue
	}

	sum := 0.0
	for _, value := range values {
		sum += math.Exp(value - maxValue)
	}
	return maxValue + math.Log(sum)
}

func validPositions(seqLen int, mask []bool) []int {
	positions := make([]int, 0, seqLen)
	for seq := 0; seq < seqLen; seq++ {
		if mask == nil || (seq < len(mask) && mask[seq]) {
			positions = append(positions, seq)
		}
	}
	return positions
}

func maskForBatch(mask [][]bool, batchIdx int) []bool {
	if mask == nil || batchIdx >= len(mask) {
		return nil
	}
	return mask[batchIdx]
}

func valueAt(values []float64, idx int) float64 {
	if idx < len(values) {
		return values[idx]
	}
	return 0
}

func matrixValueAt(values [][]float64, row, col int) float64 {
	if row < len(values) && col < len(values[row]) {
		return values[row][col]
	}
	return 0
}
