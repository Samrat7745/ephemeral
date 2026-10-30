// Package client is the Go replacement for ephemeral-java-client.
//
// Java -> Go
//
//	EphemeralEndpoint (serviceUri, application)          -> Endpoint; ActivationURL(compile) prefixes the host with the application
//	EphemeralClient.execute(activation)                  -> Client.Execute
//	EphemeralMultiClient.execute(code, inputSecretIds)   -> MultiClient.Execute (one request per endpoint, in parallel)
//	EphemeralMultiClient.execute(code, gameId, inputs)   -> MultiClient.ExecuteGame
//	Either<ActivationError, ActivationResult>            -> (*Result, error) where error is *ActivationError for HTTP failures
//	OutputConfig.AMPHORA_SECRET                          -> types.AmphoraSecret (also PlainText and SecretShare)
//
// Activation is the same type the Ephemeral service accepts (pkg/types).
package client
