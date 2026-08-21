package truth

func notWord(positive, negative uint64) (uint64, uint64) {
	return negative, positive
}

func andWord(lPositive, lNegative, rPositive, rNegative uint64) (uint64, uint64) {
	return lPositive & rPositive, lNegative | rNegative
}

func orWord(lPositive, lNegative, rPositive, rNegative uint64) (uint64, uint64) {
	return lPositive | rPositive, lNegative & rNegative
}
