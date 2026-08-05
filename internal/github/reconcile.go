package githubaudience

import (
	"context"
	"math"
)

const progressChunk = 20

func FetchAllAudienceReconciled(
	ctx context.Context,
	graphqlClient *GraphQLClient,
	restClient *RESTClient,
	pool *TokenPool,
	login string,
	audienceType AudienceType,
	onProgress ProgressFunc,
) (ReconciledAudienceResult, error) {
	graphqlResult, err := FetchAllAudience(ctx, graphqlClient, pool, login, audienceType, func(done, total int) {
		if onProgress != nil {
			t := total
			onProgress(StageGraphQL, done, &t)
		}
	})
	if err != nil {
		return ReconciledAudienceResult{}, err
	}

	byLogin := make(map[string]ProfileNode, len(graphqlResult.Nodes))
	for _, node := range graphqlResult.Nodes {
		byLogin[node.Login] = node
	}

	restLogins, err := FetchAllAudienceLoginsRest(ctx, restClient, pool, login, audienceType, func(done int) {
		if onProgress != nil {
			onProgress(StageREST, len(byLogin), nil)
		}
	})
	if err != nil {
		return ReconciledAudienceResult{}, err
	}

	var missing []string
	for l := range restLogins {
		if _, ok := byLogin[l]; !ok {
			missing = append(missing, l)
		}
	}
	reconciledTotal := len(byLogin) + len(missing)

	var recovered, unresolved []string

	if onProgress != nil {
		total := reconciledTotal
		onProgress(StageBackfill, len(byLogin), &total)
	}

	for i := 0; i < len(missing); i += progressChunk {
		end := i + progressChunk
		if end > len(missing) {
			end = len(missing)
		}
		chunk := missing[i:end]

		result, err := FetchProfilesByLoginRest(ctx, restClient, pool, chunk, BackfillConcurrency)
		if err != nil {
			return ReconciledAudienceResult{}, err
		}
		for l, node := range result.Profiles {
			byLogin[l] = node
			recovered = append(recovered, l)
		}
		unresolved = append(unresolved, result.Unresolved...)

		if onProgress != nil {
			total := reconciledTotal
			onProgress(StageBackfill, len(byLogin), &total)
		}
	}

	nodes := make([]ProfileNode, 0, len(byLogin))
	for _, node := range byLogin {
		nodes = append(nodes, node)
	}

	return ReconciledAudienceResult{
		Nodes:             nodes,
		GraphQLTotalCount: graphqlResult.TotalCount,
		RESTTotalCount:    len(restLogins),
		RecoveredLogins:   recovered,
		UnresolvedLogins:  unresolved,
	}, nil
}

func EstimateReconciliationCost(audienceCount int) ReconciliationCostEstimate {
	pages := int(math.Ceil(float64(audienceCount) / githubMaxPageSize))
	if pages < 1 {
		pages = 1
	}
	return ReconciliationCostEstimate{
		GraphQLPoints:          pages,
		RESTRequests:           pages,
		WorstCaseBackfillPoint: audienceCount,
	}
}
