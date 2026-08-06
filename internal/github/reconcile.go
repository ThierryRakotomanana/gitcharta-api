package github

import (
	"context"

	"githubaudience/internal/model"
)

const progressChunk = 20

func FetchAllAudienceReconciled(
	ctx context.Context,
	graphqlClient *GraphQLClient,
	restClient *RESTClient,
	pool *TokenPool,
	login string,
	audienceType model.AudienceType,
	onProgress model.ProgressFunc,
) (model.ReconciledAudienceResult, error) {
	graphqlResult, err := FetchAllAudience(ctx, graphqlClient, pool, login, audienceType, func(done, total int) {
		if onProgress != nil {
			t := total
			onProgress(model.StageGraphQL, done, &t)
		}
	})
	if err != nil {
		return model.ReconciledAudienceResult{}, err
	}

	byLogin := make(map[string]model.ProfileNode, len(graphqlResult.Nodes))
	for _, node := range graphqlResult.Nodes {
		byLogin[node.Login] = node
	}

	restLogins, err := FetchAllAudienceLoginsRest(ctx, restClient, pool, login, audienceType, func(done int) {
		if onProgress != nil {
			onProgress(model.StageREST, len(byLogin), nil)
		}
	})
	if err != nil {
		return model.ReconciledAudienceResult{}, err
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
		onProgress(model.StageBackfill, len(byLogin), &total)
	}

	for i := 0; i < len(missing); i += progressChunk {
		end := i + progressChunk
		if end > len(missing) {
			end = len(missing)
		}
		chunk := missing[i:end]

		result, err := FetchProfilesByLoginRest(ctx, restClient, pool, chunk, BackfillConcurrency)
		if err != nil {
			return model.ReconciledAudienceResult{}, err
		}
		for l, node := range result.Profiles {
			byLogin[l] = node
			recovered = append(recovered, l)
		}
		unresolved = append(unresolved, result.Unresolved...)

		if onProgress != nil {
			total := reconciledTotal
			onProgress(model.StageBackfill, len(byLogin), &total)
		}
	}

	nodes := make([]model.ProfileNode, 0, len(byLogin))
	for _, node := range byLogin {
		nodes = append(nodes, node)
	}

	return model.ReconciledAudienceResult{
		Nodes:             nodes,
		GraphQLTotalCount: graphqlResult.TotalCount,
		RESTTotalCount:    len(restLogins),
		RecoveredLogins:   recovered,
		UnresolvedLogins:  unresolved,
	}, nil
}
