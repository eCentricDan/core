package lib

import "strings"

// RuleError is an error type that specifies an error occurred during
// block processing that is related to a consensus rule. By checking the
// type of the error the caller can determine that the error was due to
// a consensus rule and determine which consensus rule caused the issue.
type RuleError string

const (
	RuleErrorDuplicateBlock                       RuleError = "RuleErrorDuplicateBlock"
	RuleErrorDuplicateOrphan                      RuleError = "RuleErrorDuplicateOrphan"
	RuleErrorMinDifficulty                        RuleError = "RuleErrorMinDifficulty"
	RuleErrorBlockTooBig                          RuleError = "RuleErrorBlockTooBig"
	RuleErrorNoTxns                               RuleError = "RuleErrorNoTxns"
	RuleErrorFirstTxnMustBeBlockReward            RuleError = "RuleErrorFirstTxnMustBeBlockReward"
	RuleErrorMoreThanOneBlockReward               RuleError = "RuleErrorMoreThanOneBlockReward"
	RuleErrorPreviousBlockInvalid                 RuleError = "RuleErrorPreviousBlockInvalid"
	RuleErrorPreviousBlockHeaderInvalid           RuleError = "RuleErrorPreviousBlockHeaderInvalid"
	RuleErrorTxnMustHaveAtLeastOneInput           RuleError = "RuleErrorTxnMustHaveAtLeastOneInput"
	RuleErrorTxnMustHaveAtLeastOneOutput          RuleError = "RuleErrorTxnMustHaveAtLeastOneOutput"
	RuleErrorOutputExceedsMax                     RuleError = "RuleErrorOutputExceedsMax"
	RuleErrorOutputOverflowsTotal                 RuleError = "RuleErrorOutputOverflowsTotal"
	RuleErrorTotalOutputExceedsMax                RuleError = "RuleErrorTotalOutputExceedsMax"
	RuleErrorDuplicateInputs                      RuleError = "RuleErrorDuplicateInputs"
	RuleErrorInvalidTxnMerkleRoot                 RuleError = "RuleErrorInvalidTxnMerkleRoot"
	RuleErrorDuplicateTxn                         RuleError = "RuleErrorDuplicateTxn"
	RuleErrorInputSpendsNonexistentUtxo           RuleError = "RuleErrorInputSpendsNonexistentUtxo"
	RuleErrorInputSpendsPreviouslySpentOutput     RuleError = "RuleErrorInputSpendsPreviouslySpentOutput"
	RuleErrorInputSpendsImmatureBlockReward       RuleError = "RuleErrorInputSpendsImmatureBlockReward"
	RuleErrorInputSpendsOutputWithInvalidAmount   RuleError = "RuleErrorInputSpendsOutputWithInvalidAmount"
	RuleErrorTxnOutputWithInvalidAmount           RuleError = "RuleErrorTxnOutputWithInvalidAmount"
	RuleErrorTxnOutputExceedsInput                RuleError = "RuleErrorTxnOutputExceedsInput"
	RuleErrorTxnFeeBelowNetworkMinimum            RuleError = "RuleErrorTxnFeeBelowNetworkMinimum"
	RuleErrorOverflowDetectedInFeeRateCalculation RuleError = "RuleErrorOverflowDetectedInFeeRateCalculation"
	RuleErrorBlockRewardOutputWithInvalidAmount   RuleError = "RuleErrorBlockRewardOutputWithInvalidAmount"
	RuleErrorBlockRewardOverflow                  RuleError = "RuleErrorBlockRewardOverflow"
	RuleErrorBlockRewardExceedsMaxAllowed         RuleError = "RuleErrorBlockRewardExceedsMaxAllowed"
	RuleErrorProfileUsernameExists                RuleError = "RuleErrorProfileUsernameExists"
	RuleErrorPubKeyLen                            RuleError = "RuleErrorPubKeyLen"
	RuleErrorMaxProfilePicSize                    RuleError = "RuleErrorMaxProfilePicSize"
	RuleErrorProfileCreatorPercentageSize         RuleError = "RuleErrorProfileCreatorPercentageSize"
	RuleErrorProfileStakeMultipleSize             RuleError = "RuleErrorProfileStakeMultipleSize"
	RuleErrorInvalidUsername                      RuleError = "RuleErrorInvalidUsername"
	RuleErrorEncryptedDataLen                     RuleError = "RuleErrorEncryptedDataLen"
	RuleErrorInputOverflows                       RuleError = "RuleErrorInputOverflows"
	RuleErrorInsufficientRefund                   RuleError = "RuleErrorInsufficientRefund"
	RuleErrorMissingSignature                     RuleError = "RuleErrorMissingSignature"
	RuleErrorSigHash                              RuleError = "RuleErrorSigHash"
	RuleErrorParsePublicKey                       RuleError = "RuleErrorParsePublicKey"
	RuleErrorSigCheckFailed                       RuleError = "RuleErrorSigCheckFailed"
	RuleErrorOutputPublicKeyNotRecognized         RuleError = "RuleErrorOutputPublicKeyNotRecognized"
	RuleErrorInputsWithDifferingSpendKeys         RuleError = "RuleErrorInputsWithDifferingSpendKeys"
	RuleErrorInvalidTransactionSignature          RuleError = "RuleErrorInvalidTransactionSignature"

	RuleErrorMissingBlockProducerSignature                      RuleError = "RuleErrorMissingBlockProducerSignature"
	RuleErrorInvalidBlockProducerPublicKey                      RuleError = "RuleErrorInvalidBlockProducerPublicKey"
	RuleErrorBlockProducerPublicKeyNotInWhitelist               RuleError = "RuleErrorBlockProducerPublicKeyNotInWhitelist"
	RuleErrorForbiddenBlockProducerPublicKey                    RuleError = "RuleErrorForbiddenBlockProducerPublicKey"
	RuleErrorInvalidBlockProducerSIgnature                      RuleError = "RuleErrorInvalidBlockProducerSIgnature"
	RuleErrorInvalidBlockHeader                                 RuleError = "RuleErrorInvalidBlockHeader"
	RuleErrorOrphanBlock                                        RuleError = "RuleErrorOrphanBlock"
	RuleErrorInputWithPublicKeyDifferentFromTxnPublicKey        RuleError = "RuleErrorInputWithPublicKeyDifferentFromTxnPublicKey"
	RuleErrorBlockRewardTxnNotAllowedToHaveInputs               RuleError = "RuleErrorBlockRewardTxnNotAllowedToHaveInputs"
	RuleErrorBlockRewardTxnNotAllowedToHaveSignature            RuleError = "RuleErrorBlockRewardTxnNotAllowedToHaveSignature"
	RuleErrorDeflationBombForbidsMintingAnyMoreDeSo             RuleError = "RuleErrorDeflationBombForbidsMintingAnyMoreDeSo"
	RuleErrorBitcoinExchangeShouldNotHaveInputs                 RuleError = "RuleErrorBitcoinExchangeShouldNotHaveInputs"
	RuleErrorBitcoinExchangeShouldNotHaveOutputs                RuleError = "RuleErrorBitcoinExchangeShouldNotHaveOutputs"
	RuleErrorBitcoinExchangeShouldNotHavePublicKey              RuleError = "RuleErrorBitcoinExchangeShouldNotHavePublicKey"
	RuleErrorBitcoinExchangeShouldNotHaveSignature              RuleError = "RuleErrorBitcoinExchangeShouldNotHaveSignature"
	RuleErrorBitcoinExchangeHasBadBitcoinTxHash                 RuleError = "RuleErrorBitcoinExchangeHasBadBitcoinTxHash"
	RuleErrorBitcoinExchangeDoubleSpendingBitcoinTransaction    RuleError = "RuleErrorBitcoinExchangeDoubleSpendingBitcoinTransaction"
	RuleErrorBitcoinExchangeBlockHashNotFoundInMainBitcoinChain RuleError = "RuleErrorBitcoinExchangeBlockHashNotFoundInMainBitcoinChain"
	RuleErrorBitcoinExchangeHasBadMerkleRoot                    RuleError = "RuleErrorBitcoinExchangeHasBadMerkleRoot"
	RuleErrorBitcoinExchangeInvalidMerkleProof                  RuleError = "RuleErrorBitcoinExchangeInvalidMerkleProof"
	RuleErrorBitcoinExchangeValidPublicKeyNotFoundInInputs      RuleError = "RuleErrorBitcoinExchangeValidPublicKeyNotFoundInInputs"
	RuleErrorBitcoinExchangeProblemComputingBurnOutput          RuleError = "RuleErrorBitcoinExchangeProblemComputingBurnOutput"
	RuleErrorBitcoinExchangeFeeOverflow                         RuleError = "RuleErrorBitcoinExchangeFeeOverflow"
	RuleErrorBitcoinExchangeTotalOutputLessThanOrEqualZero      RuleError = "RuleErrorBitcoinExchangeTotalOutputLessThanOrEqualZero"
	RuleErrorTxnSanity                                          RuleError = "RuleErrorTxnSanity"
	RuleErrorTxnTooBig                                          RuleError = "RuleErrorTxnTooBig"

	RuleErrorPrivateMessageEncryptedTextLengthExceedsMax           RuleError = "RuleErrorPrivateMessageEncryptedTextLengthExceedsMax"
	RuleErrorPrivateMessageRecipientPubKeyLen                      RuleError = "RuleErrorPrivateMessageRecipientPubKeyLen"
	RuleErrorPrivateMessageTstampIsZero                            RuleError = "RuleErrorPrivateMessageTstampIsZero"
	RuleErrorTransactionMissingPublicKey                           RuleError = "RuleErrorTransactionMissingPublicKey"
	RuleErrorPrivateMessageExistsWithSenderPublicKeyTstampTuple    RuleError = "RuleErrorPrivateMessageExistsWithSenderPublicKeyTstampTuple"
	RuleErrorPrivateMessageExistsWithRecipientPublicKeyTstampTuple RuleError = "RuleErrorPrivateMessageExistsWithRecipientPublicKeyTstampTuple"
	RuleErrorPrivateMessageParsePubKeyError                        RuleError = "RuleErrorPrivateMessageParsePubKeyError"
	RuleErrorPrivateMessageSenderPublicKeyEqualsRecipientPublicKey RuleError = "RuleErrorPrivateMessageSenderPublicKeyEqualsRecipientPublicKey"
	RuleErrorBurnAddressCannotBurnBitcoin                          RuleError = "RuleErrorBurnAddressCannotBurnBitcoin"

	RuleErrorFollowPubKeyLen                         RuleError = "RuleErrorFollowFollowedPubKeyLen"
	RuleErrorFollowParsePubKeyError                  RuleError = "RuleErrorFollowParsePubKeyError"
	RuleErrorFollowEntryAlreadyExists                RuleError = "RuleErrorFollowEntryAlreadyExists"
	RuleErrorFollowingNonexistentProfile             RuleError = "RuleErrorFollowingNonexistentProfile"
	RuleErrorCannotUnfollowNonexistentFollowEntry    RuleError = "RuleErrorCannotUnfollowNonexistentFollowEntry"
	RuleErrorProfilePublicKeyNotEqualToPKIDPublicKey RuleError = "RuleErrorProfilePublicKeyNotEqualToPKIDPublicKey"

	RuleErrorLikeEntryAlreadyExists            RuleError = "RuleErrorLikeEntryAlreadyExists"
	RuleErrorCannotLikeNonexistentPost         RuleError = "RuleErrorCannotLikeNonexistentPost"
	RuleErrorCannotUnlikeWithoutAnExistingLike RuleError = "RuleErrorCannotUnlikeWithoutAnExistingLike"

	RuleErrorProfileUsernameTooShort            RuleError = "RuleErrorProfileUsernameTooShort"
	RuleErrorProfileDescriptionTooShort         RuleError = "RuleErrorProfileDescriptionTooShort"
	RuleErrorProfileUsernameTooLong             RuleError = "RuleErrorProfileUsernameTooLong"
	RuleErrorProfileDescriptionTooLong          RuleError = "RuleErrorProfileDescriptionTooLong"
	RuleErrorProfileProfilePicTooShort          RuleError = "RuleErrorProfileProfilePicTooShort"
	RuleErrorProfileUpdateRequiresNonZeroInput  RuleError = "RuleErrorProfileUpdateRequiresNonZeroInput"
	RuleErrorCreateProfileTxnOutputExceedsInput RuleError = "RuleErrorCreateProfileTxnOutputExceedsInput"
	RuleErrorProfilePublicKeySize               RuleError = "RuleErrorProfilePublicKeySize"
	RuleErrorProfileBadPublicKey                RuleError = "RuleErrorProfileBadPublicKey"
	RuleErrorProfilePubKeyNotAuthorized         RuleError = "RuleErrorProfilePubKeyNotAuthorized"
	RuleErrorProfileModificationNotAuthorized   RuleError = "RuleErrorProfileModificationNotAuthorized"
	RuleErrorProfileUsernameCannotContainZeros  RuleError = "RuleErrorProfileUsernameCannotContainZeros"

	RuleSubmitPostNilParentPostHash                  RuleError = "RuleSubmitPostNilParentPostHash"
	RuleSubmitPostTitleLength                        RuleError = "RuleSubmitPostTitleLength"
	RuleSubmitPostBodyLength                         RuleError = "RuleSubmitPostBodyLength"
	RuleSubmitPostSubLength                          RuleError = "RuleSubmitPostSubLength"
	RuleErrorSubmitPostStakeMultipleSize             RuleError = "RuleErrorSubmitPostStakeMultipleSize"
	RuleErrorSubmitPostCreatorPercentageSize         RuleError = "RuleErrorSubmitPostCreatorPercentageSize"
	RuleErrorSubmitPostTimestampIsZero               RuleError = "RuleErrorSubmitPostTimestampIsZero"
	RuleErrorPostAlreadyExists                       RuleError = "RuleErrorPostAlreadyExists"
	RuleErrorSubmitPostInvalidCommentStakeID         RuleError = "RuleErrorSubmitPostInvalidCommentStakeID"
	RuleErrorSubmitPostRequiresNonZeroInput          RuleError = "RuleErrorSubmitPostRequiresNonZeroInput"
	RuleErrorSubmitPostInvalidPostHashToModify       RuleError = "RuleErrorSubmitPostInvalidPostHashToModify"
	RuleErrorSubmitPostModifyingNonexistentPost      RuleError = "RuleErrorSubmitPostModifyingNonexistentPost"
	RuleErrorSubmitPostPostModificationNotAuthorized RuleError = "RuleErrorSubmitPostPostModificationNotAuthorized"
	RuleErrorSubmitPostInvalidParentStakeIDLength    RuleError = "RuleErrorSubmitPostInvalidParentStakeIDLength"
	RuleErrorSubmitPostParentNotFound                RuleError = "RuleErrorSubmitPostParentNotFound"
	RuleErrorSubmitPostRepostPostNotFound            RuleError = "RuleErrorSubmitPostRepostPostNotFound"
	RuleErrorSubmitPostRepostOfRepost                RuleError = "RuleErrorSubmitPostRepostOfRepost"
	RuleErrorSubmitPostUpdateRepostHash              RuleError = "RuleErrorSubmitPostUpdateRepostHash"
	RuleErrorSubmitPostUpdateIsQuotedRepost          RuleError = "RuleErrorSubmitPostUpdateIsQuotedRepost"
	RuleErrorSubmitPostCannotUpdateNFT               RuleError = "RuleErrorSubmitPostCannotUpdateNFT"

	RuleErrorInvalidStakeID                      RuleError = "RuleErrorInvalidStakeID"
	RuleErrorInvalidStakeIDSize                  RuleError = "RuleErrorInvalidStakeIDSize"
	RuleErrorStakingToNonexistentPost            RuleError = "RuleErrorStakingToNonexistentPost"
	RuleErrorStakingToNonexistentProfile         RuleError = "RuleErrorStakingToNonexistentProfile"
	RuleErrorNotImplemented                      RuleError = "RuleErrorNotImplemented"
	RuleErrorStakingZeroNanosNotAllowed          RuleError = "RuleErrorStakingZeroNanosNotAllowed"
	RuleErrorAddStakeTxnMustHaveExactlyOneOutput RuleError = "RuleErrorAddStakeTxnMustHaveExactlyOneOutput"
	RuleErrorExistingStakeExceedsMaxAllowed      RuleError = "RuleErrorExistingStakeExceedsMaxAllowed"
	RuleErrorAddStakeRequiresNonZeroInput        RuleError = "RuleErrorAddStakeRequiresNonZeroInput"
	RuleErrorProfileForPostDoesNotExist          RuleError = "RuleErrorProfileForPostDoesNotExist"

	// Global Params
	RuleErrorExchangeRateTooLow                    RuleError = "RuleErrorExchangeRateTooLow"
	RuleErrorExchangeRateTooHigh                   RuleError = "RuleErrorExchangeRateTooHigh"
	RuleErrorMinNetworkFeeTooLow                   RuleError = "RuleErrorMinNetworkFeeTooLow"
	RuleErrorMinNetworkFeeTooHigh                  RuleError = "RuleErrorMinNetworkFeeTooHigh"
	RuleErrorCreateProfileFeeTooLow                RuleError = "RuleErrorCreateProfileFeeTooLow"
	RuleErrorCreateProfileTooHigh                  RuleError = "RuleErrorCreateProfileTooHigh"
	RuleErrorCreateNFTFeeTooLow                    RuleError = "RuleErrorCreateNFTFeeTooLow"
	RuleErrorCreateNFTFeeTooHigh                   RuleError = "RuleErrorCreateNFTFeeTooHigh"
	RuleErrorMaxCopiesPerNFTTooLow                 RuleError = "RuleErrorMaxCopiesPerNFTTooLow"
	RuleErrorMaxCopiesPerNFTTooHigh                RuleError = "RuleErrorMaxCopiesPerNFTTooHigh"
	RuleErrorForbiddenPubKeyLength                 RuleError = "RuleErrorForbiddenPubKeyLength"
	RuleErrorUserNotAuthorizedToUpdateExchangeRate RuleError = "RuleErrorUserNotAuthorizedToUpdateExchangeRate"
	RuleErrorUserNotAuthorizedToUpdateGlobalParams RuleError = "RuleErrorUserNotAuthorizedToUpdateGlobalParams"
	RuleErrorUserOutputMustBeNonzero               RuleError = "RuleErrorUserOutputMustBeNonzero"

	// DeSo Diamonds
	RuleErrorBasicTransferHasDiamondPostHashWithoutDiamondLevel   RuleError = "RuleErrorBasicTransferHasDiamondPostHashWithoutDiamondLevel"
	RuleErrorBasicTransferHasInvalidDiamondLevel                  RuleError = "RuleErrorBasicTransferHasInvalidDiamondLevel"
	RuleErrorBasicTransferDiamondInvalidLengthForPostHashBytes    RuleError = "RuleErrorBasicTransferInvalidLengthForPostHashBytes"
	RuleErrorBasicTransferDiamondPostEntryDoesNotExist            RuleError = "RuleErrorBasicTransferDiamondPostEntryDoesNotExist"
	RuleErrorBasicTransferInsufficientCreatorCoinsForDiamondLevel RuleError = "RuleErrorBasicTransferInsufficientCreatorCoinsForDiamondLevel"
	RuleErrorBasicTransferDiamondCannotTransferToSelf             RuleError = "RuleErrorBasicTransferDiamondCannotTransferToSelf"
	RuleErrorBasicTransferInsufficientDeSoForDiamondLevel         RuleError = "RuleErrorBasicTransferInsufficientDeSoForDiamondLevel"

	RuleErrorCoinTransferRequiresNonZeroInput                           RuleError = "RuleErrorCoinTransferRequiresNonZeroInput"
	RuleErrorCoinTransferInvalidProfilePubKeySize                       RuleError = "RuleErrorCoinTransferInvalidProfilePubKeySize"
	RuleErrorCoinTransferInvalidReceiverPubKeySize                      RuleError = "RuleErrorCoinTransferInvalidReceiverPubKeySize"
	RuleErrorCoinTransferInvalidReceiverPubKey                          RuleError = "RuleErrorCoinTransferInvalidReceiverPubKey"
	RuleErrorCoinTransferInvalidProfilePubKey                           RuleError = "RuleErrorCoinTransferInvalidProfilePubKey"
	RuleErrorCoinTransferOnNonexistentProfile                           RuleError = "RuleErrorCoinTransferOnNonexistentProfile"
	RuleErrorCoinTransferBalanceEntryDoesNotExist                       RuleError = "RuleErrorCoinTransferBalanceEntryDoesNotExist"
	RuleErrorCreatorCoinTransferMustBeGreaterThanMinThreshold           RuleError = "RuleErrorCreatorCoinTransferMustBeGreaterThanMinThreshold"
	RuleErrorCoinTransferInsufficientCoins                              RuleError = "RuleErrorCoinTransferInsufficientCoins"
	RuleErrorCoinTransferCannotTransferToSelf                           RuleError = "RuleErrorCoinTransferCannotTransferToSelf"
	RuleErrorCreatorCoinTransferHasDiamondPostHashWithoutDiamondLevel   RuleError = "RuleErrorCreatorCoinTransferHasDiamondPostHashWithoutDiamondLevel"
	RuleErrorCreatorCoinTransferCantSendDiamondsForOtherProfiles        RuleError = "RuleErrorCreatorCoinTransferCantSendDiamondsForOtherProfiles"
	RuleErrorCreatorCoinTransferCantDiamondYourself                     RuleError = "RuleErrorCreatorCoinTransferCantDiamondYourself"
	RuleErrorCreatorCoinTransferInvalidLengthForPostHashBytes           RuleError = "RuleErrorCreatorCoinTransferInvalidLengthForPostHashBytes"
	RuleErrorCreatorCoinTransferInsufficientCreatorCoinsForDiamondLevel RuleError = "RuleErrorCreatorCoinTransferInsufficientCreatorCoinsForDiamondLevel"
	RuleErrorCreatorCoinTransferHasInvalidDiamondLevel                  RuleError = "RuleErrorCreatorCoinTransferHasInvalidDiamondLevel"
	RuleErrorCreatorCoinTransferHasDiamondsAfterDeSoBlockHeight         RuleError = "RuleErrorCreatorCoinTransferHasDiamondsAfterDeSoBlockHeight"
	RuleErrorCreatorCoinTransferPostAlreadyHasSufficientDiamonds        RuleError = "RuleErrorCreatorCoinTransferPostAlreadyHasSufficientDiamonds"
	RuleErrorCreatorCoinTransferDiamondsCantHaveNegativeNanos           RuleError = "RuleErrorCreatorCoinTransferDiamondsCantHaveNegativeNanos"
	RuleErrorCreatorCoinTransferDiamondPostEntryDoesNotExist            RuleError = "RuleErrorCreatorCoinTransferDiamondPostEntryDoesNotExist"

	RuleErrorCreatorCoinRequiresNonZeroInput                           RuleError = "RuleErrorCreatorCoinRequiresNonZeroInput"
	RuleErrorCreatorCoinInvalidPubKeySize                              RuleError = "RuleErrorCreatorCoinInvalidPubKeySize"
	RuleErrorCreatorCoinOperationOnNonexistentProfile                  RuleError = "RuleErrorCreatorCoinOperationOnNonexistentProfile"
	RuleErrorCreatorCoinBuyMustTradeNonZeroDeSo                        RuleError = "RuleErrorCreatorCoinBuyMustTradeNonZeroDeSo"
	RuleErrorCreatorCoinTxnOutputWithInvalidBuyAmount                  RuleError = "RuleErrorCreatorCoinTxnOutputWithInvalidBuyAmount"
	RuleErrorCreatorCoinTxnOutputExceedsInput                          RuleError = "RuleErrorCreatorCoinTxnOutputExceedsInput"
	RuleErrorCreatorCoinLessThanMinimumSetByUser                       RuleError = "RuleErrorCreatorCoinLessThanMinimumSetByUser"
	RuleErrorCreatorCoinBuyMustTradeNonZeroDeSoAfterFees               RuleError = "RuleErrorCreatorCoinBuyMustTradeNonZeroDeSoAfterFees"
	RuleErrorCreatorCoinBuyMustTradeNonZeroDeSoAfterFounderReward      RuleError = "RuleErrorCreatorCoinBuyMustTradeNonZeroDeSoAfterFounderReward"
	RuleErrorCreatorCoinBuyMustSatisfyAutoSellThresholdNanos           RuleError = "RuleErrorCreatorCoinBuyMustSatisfyAutoSellThresholdNanos"
	RuleErrorCreatorCoinBuyMustSatisfyAutoSellThresholdNanosForCreator RuleError = "RuleErrorCreatorCoinBuyMustSatisfyAutoSellThresholdNanosForCreator"
	RuleErrorCreatorCoinBuyMustSatisfyAutoSellThresholdNanosForBuyer   RuleError = "RuleErrorCreatorCoinBuyMustSatisfyAutoSellThresholdNanosForBuyer"
	RuleErrorCreatorCoinBuyZeroLockedNanosAndNonZeroHolders            RuleError = "RuleErrorCreatorCoinBuyZeroLockedNanosAndNonZeroHolders"
	RuleErrorCreatorCoinSellMustHaveAtLeastOneInput                    RuleError = "RuleErrorCreatorCoinSellMustHaveAtLeastOneInput"
	RuleErrorCreatorCoinSellMustTradeNonZeroCreatorCoin                RuleError = "RuleErrorCreatorCoinSellMustTradeNonZeroCreatorCoin"
	RuleErrorCreatorCoinSellerBalanceEntryDoesNotExist                 RuleError = "RuleErrorCreatorCoinSellerBalanceEntryDoesNotExist"
	RuleErrorCreatorCoinSellInsufficientCoins                          RuleError = "RuleErrorCreatorCoinSellInsufficientCoins"
	RuleErrorCreatorCoinSellNotAllowedWhenZeroDeSoLocked               RuleError = "RuleErrorCreatorCoinSellNotAllowedWhenZeroDeSoLocked"
	RuleErrorDeSoReceivedIsLessThanMinimumSetBySeller                  RuleError = "RuleErrorDeSoReceivedIsLessThanMinimumSetBySeller"

	// DAO Coins
	RuleErrorDAOCoinRequiresNonZeroInput                  RuleError = "RuleErrorDAOCoinRequiresNonZeroInput"
	RuleErrorDAOCoinInvalidPubKeySize                     RuleError = "RuleErrorDAOCoinInvalidPubKeySize"
	RuleErrorDAOCoinInvalidPubKey                         RuleError = "RuleErrorDAOCoinInvalidPubKey"
	RuleErrorDAOCoinOperationOnNonexistentProfile         RuleError = "RuleErrorDAOCoinOperationOnNonexistentProfile"
	RuleErrorDAOCoinBurnMustBurnNonZeroDAOCoin            RuleError = "RuleErrorDAOCoinBurnMustBurnNonZeroDAOCoin"
	RuleErrorDAOCoinBurnerBalanceEntryDoesNotExist        RuleError = "RuleErrorDAOCoinBurnerBalanceEntryDoesNotExist"
	RuleErrorDAOCoinBurnInsufficientCoins                 RuleError = "RuleErrorDAOCoinBurnInsufficientCoins"
	RuleErrorOnlyProfileOwnerCanMintDAOCoin               RuleError = "RuleErrorOnlyProfileOwnerCanMintDAOCoin"
	RuleErrorDAOCoinMustMintNonZeroDAOCoin                RuleError = "RuleErrorDAOCoinMustMintNonZeroDAOCoin"
	RuleErrorOverflowWhileMintingDAOCoins                 RuleError = "RuleErrorOverflowWhileMintingDAOCoins"
	RuleErrorDAOCoinBurnAmountExceedsCoinsInCirculation   RuleError = "RuleErrorDAOCoinBurnAmountExceedsCoinsInCirculation"
	RuleErrorDAOCoinBeforeDAOCoinBlockHeight              RuleError = "RuleErrorDAOCoinBeforeDAOCoinBlockHeight"
	RuleErrorDAOCoinCannotDisableMintingIfAlreadyDisabled RuleError = "RuleErrorDAOCoinCannotDisableMintingIfAlreadyDisabled"
	RuleErrorDAOCoinCannotMintIfMintingIsDisabled         RuleError = "RuleErrorDAOCoinCannotMintIfMintingIsDisabled"
	RuleErrorOnlyProfileOwnerCanDisableMintingDAOCoin     RuleError = "RuleErrorOnlyProfileOwnerCanDisableMintingDAOCoin"
	RuleErrorDAOCoinTransferProfileOwnerOnlyViolation     RuleError = "RuleErrorDAOCoinTransferProfileOwnerOnlyViolation"
	RuleErrorDAOCoinTransferDAOMemberOnlyViolation    	  RuleError = "RuleErrorDAOCoinTransferDAOMemberOnlyViolation"

	// DAO Coin Transfer Restrictions
	RuleErrorOnlyProfileOwnerCanUpdateTransferRestrictionStatus                    RuleError = "RuleErrorOnlyProfileOwnerCanUpdateTransferRestrictionStatus"
	RuleErrorDAOCoinCannotUpdateRestrictionStatusIfStatusIsPermanentlyUnrestricted RuleError = "RuleErrorDAOCoinCannotUpdateRestrictionStatusIfStatusIsPermanentlyUnrestricted"
	RuleErrorDAOCoinCannotUpdateTransferRestrictionStatusToCurrentStatus           RuleError = "RuleErrorDAOCoinCannotUpdateTransferRestrictionStatusToCurrentStatus"

	// Derived Keys
	RuleErrorAuthorizeDerivedKeyAccessSignatureNotValid RuleError = "RuleErrorAuthorizeDerivedKeyAccessSignatureNotValid"
	RuleErrorAuthorizeDerivedKeyRequiresNonZeroInput    RuleError = "RuleErrorAuthorizeDerivedKeyRequiresNonZeroInput"
	RuleErrorAuthorizeDerivedKeyExpiredDerivedPublicKey RuleError = "RuleErrorAuthorizeDerivedKeyExpired"
	RuleErrorAuthorizeDerivedKeyInvalidDerivedPublicKey RuleError = "RuleErrorAuthorizeDerivedKeyInvalidDerivedKey"
	RuleErrorAuthorizeDerivedKeyDeletedDerivedPublicKey RuleError = "RuleErrorAuthorizeDerivedKeyDeletedDerivedPublicKey"
	RuleErrorAuthorizeDerivedKeyInvalidOwnerPublicKey   RuleError = "RuleErrorAuthorizeDerivedKeyInvalidOwnerPublicKey"
	RuleErrorDerivedKeyNotAuthorized                    RuleError = "RuleErrorDerivedKeyNotAuthorized"
	RuleErrorDerivedKeyInvalidExtraData                 RuleError = "RuleErrorDerivedKeyInvalidExtraData"
	RuleErrorDerivedKeyBeforeBlockHeight                RuleError = "RuleErrorDerivedKeyBeforeBlockHeight"

	// NFTs
	RuleErrorTooManyNFTCopies                            RuleError = "RuleErrorTooManyNFTCopies"
	RuleErrorCreateNFTRequiresNonZeroInput               RuleError = "RuleErrorCreateNFTRequiresNonZeroInput"
	RuleErrorUpdateNFTRequiresNonZeroInput               RuleError = "RuleErrorUpdateNFTRequiresNonZeroInput"
	RuleErrorCreateNFTOnNonexistentPost                  RuleError = "RuleErrorCreateNFTOnNonexistentPost"
	RuleErrorCreateNFTOnVanillaRepost                    RuleError = "RuleErrorCreateNFTOnVanillaRepost"
	RuleErrorCreateNFTWithInsufficientFunds              RuleError = "RuleErrorCreateNFTWithInsufficientFunds"
	RuleErrorCreateNFTOnPostThatAlreadyIsNFT             RuleError = "RuleErrorCreateNFTOnPostThatAlreadyIsNFT"
	RuleErrorCannotHaveUnlockableAndBuyNowNFT            RuleError = "RuleErrorCannotHaveUnlockableAndBuyNowNFT"
	RuleErrorCannotHaveBuyNowPriceBelowMinBidAmountNanos RuleError = "RuleErrorCannotHaveBuyNowPriceBelowMinBidAmountNanos"
	RuleErrorCreateNFTMustBeCalledByPoster               RuleError = "RuleErrorCreateNFTMustBeCalledByPoster"
	RuleErrorNFTMustHaveNonZeroCopies                    RuleError = "RuleErrorNFTMustHaveNonZeroCopies"
	RuleErrorCannotUpdateNonExistentNFT                  RuleError = "RuleErrorCannotUpdateNonExistentNFT"
	RuleErrorCannotUpdatePendingNFTTransfer              RuleError = "RuleErrorCannotUpdatePendingNFTTransfer"
	RuleErrorCannotAcceptBidForPendingNFTTransfer        RuleError = "RuleErrorCannotAcceptBidForPendingNFTTransfer"
	RuleErrorCannotBidForPendingNFTTransfer              RuleError = "RuleErrorCannotBidForPendingNFTTransfer"
	RuleErrorUpdateNFTByNonOwner                         RuleError = "RuleErrorUpdateNFTByNonOwner"
	RuleErrorAcceptNFTBidByNonOwner                      RuleError = "RuleErrorAcceptNFTBidByNonOwner"
	RuleErrorCantCreateNFTWithoutProfileEntry            RuleError = "RuleErrorCantCreateNFTWithoutProfileEntry"
	RuleErrorNFTRoyaltyHasTooManyBasisPoints             RuleError = "RuleErrorNFTRoyaltyHasTooManyBasisPoints"
	RuleErrorNFTRoyaltyOverflow                          RuleError = "RuleErrorNFTRoyaltyOverflow"
	RuleErrorNFTUpdateMustUpdateIsForSaleStatus          RuleError = "RuleErrorNFTUpdateMustUpdateIsForSaleStatus"
	RuleErrorBuyNowNFTBeforeBlockHeight                  RuleError = "RuleErrorBuyNowNFTBeforeBlockHeight"

	// NFT Bids
	RuleErrorNFTBidRequiresNonZeroInput                    RuleError = "RuleErrorNFTBidRequiresNonZeroInput"
	RuleErrorAcceptNFTBidRequiresNonZeroInput              RuleError = "RuleErrorAcceptNFTBidRequiresNonZeroInput"
	RuleErrorNFTBidTxnOutputWithInvalidBidAmount           RuleError = "RuleErrorNFTBidTxnOutputWithInvalidBidAmount"
	RuleErrorBuyNowNFTBidTxnOutputExceedsInput             RuleError = "RuleErrorBuyNowNFTBidTxnOutputExceedsInput"
	RuleErrorBuyNowNFTBidMustBidNonZeroDeSo                RuleError = "RuleErrorBuyNowNFTBidMustBidNonZeroDeSo"
	RuleErrorBuyNowNFTBidMustHaveMinBidAmountNanos         RuleError = "RuleErrorBuyNowNFTBidMustHaveMinBidAmountNanos"
	RuleErrorNFTBidOnNonExistentPost                       RuleError = "RuleErrorNFTBidOnNonExistentPost"
	RuleErrorNFTBidOnPostThatIsNotAnNFT                    RuleError = "RuleErrorNFTBidOnPostThatIsNotAnNFT"
	RuleErrorNFTBidOnInvalidSerialNumber                   RuleError = "RuleErrorNFTBidOnInvalidSerialNumber"
	RuleErrorNFTBidOnNonExistentNFTEntry                   RuleError = "RuleErrorNFTBidOnNonExistentNFTEntry"
	RuleErrorNFTBidOnNFTThatIsNotForSale                   RuleError = "RuleErrorNFTBidOnNFTThatIsNotForSale"
	RuleErrorNFTOwnerCannotBidOnOwnedNFT                   RuleError = "RuleErrorNFTOwnerCannotBidOnOwnedNFT"
	RuleErrorCantAcceptNonExistentBid                      RuleError = "RuleErrorCantAcceptNonExistentBid"
	RuleErrorAcceptedNFTBidAmountDoesNotMatch              RuleError = "RuleErrorAcceptedNFTBidAmountDoesNotMatch"
	RuleErrorPostEntryNotFoundForAcceptedNFTBid            RuleError = "RuleErrorPostEntryNotFoundForAcceptedNFTBid"
	RuleErrorUnlockableNFTMustProvideUnlockableText        RuleError = "RuleErrorUnlockableNFTMustProvideUnlockableText"
	RuleErrorUnlockableTextLengthExceedsMax                RuleError = "RuleErrorUnlockableTextLengthExceedsMax"
	RuleErrorAcceptedNFTBidMustSpecifyBidderInputs         RuleError = "RuleErrorAcceptedNFTBidMustSpecifyBidderInputs"
	RuleErrorBidderInputForAcceptedNFTBidNoLongerExists    RuleError = "RuleErrorBidderInputForAcceptedNFTBidNoLongerExists"
	RuleErrorAcceptNFTBidderInputsInsufficientForBidAmount RuleError = "RuleErrorAcceptNFTBidderInputsInsufficientForBidAmount"
	RuleErrorInsufficientFundsForNFTBid                    RuleError = "RuleErrorInsufficientFundsForNFTBid"
	RuleErrorNFTBidLessThanMinBidAmountNanos               RuleError = "RuleErrorNFTBidLessThanMinBidAmountNanos"
	RuleErrorZeroBidOnBuyNowNFT                            RuleError = "RuleErrorZeroBidOnBuyNowNFT"

	// NFT Transfers
	RuleErrorNFTTransferBeforeBlockHeight                 RuleError = "RuleErrorNFTTranserBeforeBlockHeight"
	RuleErrorAcceptNFTTransferBeforeBlockHeight           RuleError = "RuleErrorAcceptNFTTranserBeforeBlockHeight"
	RuleErrorNFTTransferInvalidReceiverPubKeySize         RuleError = "RuleErrorNFTTransferInvalidReceiverPubKeySize"
	RuleErrorNFTTransferCannotTransferToSelf              RuleError = "RuleErrorNFTTransferCannotTransferToSelf"
	RuleErrorCannotTransferNonExistentNFT                 RuleError = "RuleErrorCannotTransferNonExistentNFT"
	RuleErrorNFTTransferByNonOwner                        RuleError = "RuleErrorNFTTransferByNonOwner"
	RuleErrorCannotTransferForSaleNFT                     RuleError = "RuleErrorCannotTransferForSaleNFT"
	RuleErrorCannotTransferUnlockableNFTWithoutUnlockable RuleError = "RuleErrorCannotTransferUnlockableNFTWithoutUnlockable"
	RuleErrorNFTTransferRequiresNonZeroInput              RuleError = "RuleErrorNFTTransferRequiresNonZeroInput"
	RuleErrorCannotAcceptTransferOfNonExistentNFT         RuleError = "RuleErrorCannotAcceptTransferOfNonExistentNFT"
	RuleErrorAcceptNFTTransferByNonOwner                  RuleError = "RuleErrorAcceptNFTTransferByNonOwner"
	RuleErrorAcceptNFTTransferForNonPendingNFT            RuleError = "RuleErrorAcceptNFTTransferForNonPendingNFT"
	RuleErrorAcceptNFTTransferRequiresNonZeroInput        RuleError = "RuleErrorAcceptNFTTransferRequiresNonZeroInput"

	// NFT Burns
	RuleErrorBurnNFTBeforeBlockHeight    RuleError = "RuleErrorBurnNFTBeforeBlockHeight"
	RuleErrorCannotBurnNonExistentNFT    RuleError = "RuleErrorCannotBurnNonExistentNFT"
	RuleErrorBurnNFTByNonOwner           RuleError = "RuleErrorBurnNFTByNonOwner"
	RuleErrorCannotBurnNFTThatIsForSale  RuleError = "RuleErrorCannotBurnNFTThatIsForSale"
	RuleErrorBurnNFTRequiresNonZeroInput RuleError = "RuleErrorBurnNFTRequiresNonZeroInput"

	RuleErrorSwapIdentityIsParamUpdaterOnly RuleError = "RuleErrorSwapIdentityIsParamUpdaterOnly"
	RuleErrorFromPublicKeyIsRequired        RuleError = "RuleErrorFromPublicKeyIsRequired"
	RuleErrorInvalidFromPublicKey           RuleError = "RuleErrorInvalidFromPublicKey"
	RuleErrorToPublicKeyIsRequired          RuleError = "RuleErrorToPublicKeyIsRequired"
	RuleErrorInvalidToPublicKey             RuleError = "RuleErrorInvalidToPublicKey"
	RuleErrorOldFromPublicKeyHasDeletedPKID RuleError = "RuleErrorOldFromPublicKeyHasDeletedPKID"
	RuleErrorOldToPublicKeyHasDeletedPKID   RuleError = "RuleErrorOldToPublicKeyHasDeletedPKID"

	HeaderErrorDuplicateHeader                                                   RuleError = "HeaderErrorDuplicateHeader"
	HeaderErrorNilPrevHash                                                       RuleError = "HeaderErrorNilPrevHash"
	HeaderErrorInvalidParent                                                     RuleError = "HeaderErrorInvalidParent"
	HeaderErrorBlockTooFarInTheFuture                                            RuleError = "HeaderErrorBlockTooFarInTheFuture"
	HeaderErrorTimestampTooEarly                                                 RuleError = "HeaderErrorTimestampTooEarly"
	HeaderErrorBlockDifficultyAboveTarget                                        RuleError = "HeaderErrorBlockDifficultyAboveTarget"
	HeaderErrorHeightInvalid                                                     RuleError = "HeaderErrorHeightInvalid"
	HeaderErrorDifficultyBitsNotConsistentWithTargetDifficultyComputedFromParent RuleError = "HeaderErrorDifficultyBitsNotConsistentWithTargetDifficultyComputedFromParent"

	TxErrorTooLarge                                                 RuleError = "TxErrorTooLarge"
	TxErrorDuplicate                                                RuleError = "TxErrorDuplicate"
	TxErrorIndividualBlockReward                                    RuleError = "TxErrorIndividualBlockReward"
	TxErrorInsufficientFeeMinFee                                    RuleError = "TxErrorInsufficientFeeMinFee"
	TxErrorInsufficientFeeRateLimit                                 RuleError = "TxErrorInsufficientFeeRateLimit"
	TxErrorInsufficientFeePriorityQueue                             RuleError = "TxErrorInsufficientFeePriorityQueue"
	TxErrorUnconnectedTxnNotAllowed                                 RuleError = "TxErrorUnconnectedTxnNotAllowed"
	TxErrorCannotProcessBitcoinExchangeUntilBitcoinManagerIsCurrent RuleError = "TxErrorCannotProcessBitcoinExchangeUntilBitcoinManagerIsCurrent"
)

func (e RuleError) Error() string {
	return string(e)
}

// IsRuleError returns true if the error is any of the errors specified above.
func IsRuleError(err error) bool {
	// TODO: I know I am a bad person for doing a string comparison here, but I
	// realized late in the game that errors.Wrapf warps the type of what it contains
	// and moving this from a type-switch to a string compare is easier than going
	// back and expunging all instances of Wrapf that might cause us to lose the
	// type of RuleError randomly as the error gets passed up the stack. Nevertheless,
	// eventually we should clean this up and get rid of the string comparison both
	// for the code's sake but also for the sake of our tests.
	return strings.Contains(err.Error(), "RuleError") ||
		strings.Contains(err.Error(), "HeaderError") ||
		strings.Contains(err.Error(), "TxError")
}
