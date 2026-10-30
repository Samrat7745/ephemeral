// Copyright (c) 2026 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0

// Package discovery implements the discovery service that pairs the MPC players of a game and creates the network
// (Istio Gateway, VirtualService, Service) that connects the pods of the different clusters.
//
// Bookkeeping: ServiceNG keeps the network and port of a pod (networks, pods) for the lifetime of the pod, the players
// and the game FSM per game id (players, games). Consecutive games on the same pod therefore reuse the pod's network
// and port; per-game state is not removed when a game finishes. See docs/consecutive-games.md (SPECS-2171) for the
// analysis of what is and is not known about consecutive games, and the tests in consecutive_games_test.go and
// networker_test.go that pin the behaviour (skipped tests carry "known: SPECS-2171").
package discovery
