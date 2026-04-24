package openaicompat

import (
	"encoding/json"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

// ResponsesRequestToChatCompletionsRequest converts an OpenAI Responses API request
// to a Chat Completions API request for models that don't support Responses API natively.
func ResponsesRequestToChatCompletionsRequest(req *dto.OpenAIResponsesRequest) (*dto.GeneralOpenAIRequest, error) {
	if req == nil {
		return nil, errors.New("request is nil")
	}
	if req.Model == "" {
		return nil, errors.New("model is required")
	}

	chatReq := &dto.GeneralOpenAIRequest{
		Model:             req.Model,
		Stream:            req.Stream,
		Temperature:       req.Temperature,
		TopP:              req.TopP,
		MaxCompletionTokens: req.MaxOutputTokens,
	}

	// Parse and convert messages from Instructions first (system prompt)
	var messages []dto.Message
	if req.Instructions != nil {
		var instructions string
		if err := common.Unmarshal(req.Instructions, &instructions); err == nil && instructions != "" {
			messages = append(messages, dto.Message{
				Role:    "system",
				Content: instructions,
			})
		}
	}

	// Parse and convert Input to messages
	if req.Input != nil {
		// Try string input first
		if common.GetJsonType(req.Input) == "string" {
			var inputStr string
			if err := common.Unmarshal(req.Input, &inputStr); err == nil && inputStr != "" {
				messages = append(messages, dto.Message{
					Role:    "user",
					Content: inputStr,
				})
			}
		} else if common.GetJsonType(req.Input) == "array" {
			// Try array of message items
			var inputs []json.RawMessage
			if err := common.Unmarshal(req.Input, &inputs); err == nil {
				for _, inputItem := range inputs {
					// Parse each input item
					var itemMap map[string]any
					if err := common.Unmarshal(inputItem, &itemMap); err != nil {
						continue
					}

					// Handle message type items
					itemType, _ := itemMap["type"].(string)
					if itemType == "message" || itemType == "" {
						role, _ := itemMap["role"].(string)
						if role == "" {
							role = "user"
						}

						// Handle content - can be string or array
						contentRaw, hasContent := itemMap["content"]
						if !hasContent {
							continue
						}

						// Map developer role to system
						if role == "developer" {
							role = "system"
						}

						// Check if content is string
						if contentStr, ok := contentRaw.(string); ok {
							messages = append(messages, dto.Message{
								Role:    role,
								Content: contentStr,
							})
						} else if contentArr, ok := contentRaw.([]any); ok {
							// Content is array of parts (input_text, output_text, etc.)
							var textContent string
							for _, part := range contentArr {
								if partMap, ok := part.(map[string]any); ok {
									partType, _ := partMap["type"].(string)
									if partType == "input_text" || partType == "output_text" {
										if text, ok := partMap["text"].(string); ok {
											if textContent != "" {
												textContent += "\n"
											}
											textContent += text
										}
									}
								}
							}
							if textContent != "" {
								messages = append(messages, dto.Message{
									Role:    role,
									Content: textContent,
								})
							}
						}
					}
				}
			}
		}
	}

	chatReq.Messages = messages

	// Handle tools and tool_choice (skip for now - requires complex conversion)
	_ = req.Tools
	_ = req.ToolChoice

	// Handle stream_options
	if req.StreamOptions != nil {
		chatReq.StreamOptions = req.StreamOptions
	}

	return chatReq, nil
}

// ResponsesResponseToChatCompletionsResponse converts Responses API response to Chat Completions response
// This is a simplified version for compatibility
func ResponsesResponseToChatCompletionsResponse(resp *dto.OpenAIResponsesResponse, id string) (*dto.OpenAITextResponse, *dto.Usage, error) {
	// Find output text
	outputText := ""
	if resp != nil && len(resp.Output) > 0 {
		for _, output := range resp.Output {
			if output.Content != nil && len(output.Content) > 0 {
				for _, content := range output.Content {
					if content.Type == "output_text" && content.Text != "" {
						outputText = content.Text
						break
					}
				}
			}
		}
	}

	chatResp := &dto.OpenAITextResponse{
		Id:      id,
		Object:  "chat.completion",
		Created: resp.CreatedAt,
		Model:   resp.Model,
		Choices: []dto.OpenAITextResponseChoice{
			{
				Index: 0,
				Message: dto.Message{
					Role:    "assistant",
					Content: outputText,
				},
			},
		},
	}

	var usage *dto.Usage
	if resp.Usage != nil {
		usage = &dto.Usage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		}
		chatResp.Usage = *usage
	}

	return chatResp, usage, nil
}

// ExtractOutputTextFromResponses extracts the output text from a Responses API response
func ExtractOutputTextFromResponses(resp *dto.OpenAIResponsesResponse) string {
	if resp == nil || len(resp.Output) == 0 {
		return ""
	}

	for _, output := range resp.Output {
		if output.Content != nil && len(output.Content) > 0 {
			for _, content := range output.Content {
				if content.Type == "output_text" && content.Text != "" {
					return content.Text
				}
			}
		}
	}
	return ""
}
