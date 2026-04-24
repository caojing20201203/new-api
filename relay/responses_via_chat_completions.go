package relay

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	openaichannel "github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// generateResponseID generates a response ID similar to OpenAI's format
func generateResponseID() string {
	return "resp_" + uuid.New().String()[:24]
}

// ChatCompletionsStreamToResponsesStreamHandler converts Chat Completions SSE stream to Responses API stream format
func ChatCompletionsStreamToResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	defer service.CloseResponseBodyGracefully(resp)

	responseID := generateResponseID()
	createdAt := time.Now().Unix()
	itemID := "item_0"
	model := info.UpstreamModelName

	var (
		usage      = &dto.Usage{}
		outputText strings.Builder
		streamErr  *types.NewAPIError
	)

	// Send response.created event
	createdEvent := map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id":         responseID,
			"object":     "response",
			"created_at": createdAt,
			"model":      model,
			"status":     "in_progress",
			"output":     []any{},
			"usage":      nil,
		},
	}
	if err := helper.ObjectData(c, createdEvent); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	// Send output_item.added event
	itemAddedEvent := map[string]any{
		"type":         "response.output_item.added",
		"item_id":      itemID,
		"output_index": 0,
		"item": map[string]any{
			"type":    "message",
			"id":      itemID,
			"role":    "assistant",
			"content": []any{},
		},
	}
	if err := helper.ObjectData(c, itemAddedEvent); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	// Send content_part.added event
	contentAddedEvent := map[string]any{
		"type":         "response.content_part.added",
		"item_id":      itemID,
		"output_index": 0,
		"content_index": 0,
		"part": map[string]any{
			"type": "output_text",
			"text": "",
		},
	}
	if err := helper.ObjectData(c, contentAddedEvent); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		if streamErr != nil {
			sr.Stop(streamErr)
			return
		}

		// Skip [DONE] marker
		if data == "[DONE]" {
			return
		}

		var chatChunk dto.ChatCompletionsStreamResponse
		if err := common.UnmarshalJsonStr(data, &chatChunk); err != nil {
			// Silently skip invalid JSON chunks
			return
		}

		// Update model from response if available
		if chatChunk.Model != "" {
			model = chatChunk.Model
		}

		// Update usage if present (Azure, etc.)
		if chatChunk.Usage != nil {
			usage.PromptTokens = chatChunk.Usage.PromptTokens
			usage.CompletionTokens = chatChunk.Usage.CompletionTokens
			usage.TotalTokens = chatChunk.Usage.TotalTokens
		}

		// Process choices for text content
		if len(chatChunk.Choices) > 0 {
			choice := chatChunk.Choices[0]

			// Handle content delta
			if choice.Delta.Content != nil && *choice.Delta.Content != "" {
				deltaText := *choice.Delta.Content
				outputText.WriteString(deltaText)

				// Send output_text.delta event
				deltaEvent := map[string]any{
					"type":         "response.output_text.delta",
					"item_id":      itemID,
					"output_index": 0,
					"content_index": 0,
					"delta":        deltaText,
				}
				if err := helper.ObjectData(c, deltaEvent); err != nil {
					streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
					sr.Stop(streamErr)
					return
				}
			}

			// Handle finish_reason
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				// Check for usage from OpenAI-style final chunk with usage
				if chatChunk.Usage != nil {
					usage.PromptTokens = chatChunk.Usage.PromptTokens
					usage.CompletionTokens = chatChunk.Usage.CompletionTokens
					usage.TotalTokens = chatChunk.Usage.TotalTokens
				}
			}
		}
	})

	if streamErr != nil {
		return nil, streamErr
	}

	// Send output_item.done event
	itemDoneEvent := map[string]any{
		"type":         "response.output_item.done",
		"item_id":      itemID,
		"output_index": 0,
		"item": map[string]any{
			"type":    "message",
			"id":      itemID,
			"role":    "assistant",
			"content": []map[string]any{
				{
					"type": "output_text",
					"text": outputText.String(),
				},
			},
		},
	}
	if err := helper.ObjectData(c, itemDoneEvent); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	// Calculate usage if not provided
	if usage.TotalTokens == 0 {
		usage = service.ResponseText2Usage(c, outputText.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
	}

	// Send completed event
	completedEvent := map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":         responseID,
			"object":     "response",
			"created_at": createdAt,
			"model":      model,
			"status":     "completed",
			"output": []map[string]any{
				{
					"type":    "message",
					"id":      itemID,
					"role":    "assistant",
					"content": []map[string]any{
						{
							"type": "output_text",
							"text": outputText.String(),
						},
					},
				},
			},
			"usage": map[string]any{
				"input_tokens":  usage.PromptTokens,
				"output_tokens": usage.CompletionTokens,
				"total_tokens":  usage.TotalTokens,
			},
		},
	}
	if err := helper.ObjectData(c, completedEvent); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	helper.Done(c)
	return usage, nil
}

// responsesViaChatCompletions handles Responses API requests by converting them to Chat Completions
// This is useful for models that don't support the Responses API natively but support Chat Completions
func responsesViaChatCompletions(c *gin.Context, info *relaycommon.RelayInfo, responsesReq *dto.OpenAIResponsesRequest) (*dto.Usage, *types.NewAPIError) {
	// Convert Responses API request to Chat Completions request
	chatReq, err := service.ResponsesRequestToChatCompletionsRequest(responsesReq)
	if err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	info.AppendRequestConversion(types.RelayFormatOpenAI)

	// Apply model mapping
	err = helper.ModelMappedHelper(c, info, chatReq)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	// Get adaptor for upstream channel - use OpenAI adaptor for Chat Completions format
	adaptor := &openaichannel.Adaptor{
		ChannelType: constant.ChannelTypeOpenAI,
	}
	adaptor.Init(info)

	// Override the request URL to use Chat Completions endpoint instead of Responses
	info.RequestURLPath = "/v1/chat/completions"

	// Marshal the chat request
	jsonData, err := common.Marshal(chatReq)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}

	// Remove disabled fields
	jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}

	// Apply param override if configured
	if len(info.ParamOverride) > 0 {
		jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeChannelParamOverrideInvalid, types.ErrOptionWithSkipRetry())
		}
	}

	// Do request to upstream
	var httpResp *http.Response
	resp, err := adaptor.DoRequest(c, info, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	if resp == nil {
		return nil, types.NewOpenAIError(nil, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")

	httpResp = resp.(*http.Response)
	info.IsStream = info.IsStream || strings.HasPrefix(httpResp.Header.Get("Content-Type"), "text/event-stream")

	if httpResp.StatusCode != http.StatusOK {
		newApiErr := service.RelayErrorHandler(c.Request.Context(), httpResp, false)
		service.ResetStatusCode(newApiErr, statusCodeMappingStr)
		return nil, newApiErr
	}

	// Handle streaming response - convert Chat Completions stream to Responses API stream
	if info.IsStream {
		usage, newApiErr := ChatCompletionsStreamToResponsesStreamHandler(c, info, httpResp)
		if newApiErr != nil {
			service.ResetStatusCode(newApiErr, statusCodeMappingStr)
			return nil, newApiErr
		}
		return usage, nil
	}

	// Handle non-streaming response - manually parse and convert to Responses API format
	defer service.CloseResponseBodyGracefully(httpResp)

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}

	var chatResp dto.OpenAITextResponse
	if err := common.Unmarshal(body, &chatResp); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	// Convert Chat Completions response to Responses API response format
	responseID := generateResponseID()
	itemID := "item_0"
	createdAt := time.Now().Unix()
	outputText := ""

	if len(chatResp.Choices) > 0 {
		outputText = chatResp.Choices[0].Message.StringContent()
	}

	responsesResp := map[string]any{
		"id":         responseID,
		"object":     "response",
		"created_at": createdAt,
		"model":      info.UpstreamModelName,
		"status":     "completed",
		"output": []map[string]any{
			{
				"type":    "message",
				"id":      itemID,
				"role":    "assistant",
				"content": []map[string]any{
					{
						"type": "output_text",
						"text": outputText,
					},
				},
			},
		},
		"usage": map[string]any{
			"input_tokens":  chatResp.Usage.PromptTokens,
			"output_tokens": chatResp.Usage.CompletionTokens,
			"total_tokens":  chatResp.Usage.TotalTokens,
		},
	}

	responseBody, err := common.Marshal(responsesResp)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}

	service.IOCopyBytesGracefully(c, httpResp, responseBody)

	usage := &dto.Usage{
		PromptTokens:     chatResp.Usage.PromptTokens,
		CompletionTokens: chatResp.Usage.CompletionTokens,
		TotalTokens:      chatResp.Usage.TotalTokens,
	}

	return usage, nil
}
