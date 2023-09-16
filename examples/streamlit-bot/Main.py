import streamlit as st
import time
import requests
import json

def ask(prompt):
    url = 'http://localhost:8080/v1/chat/completions'
    myobj = {
        "model": "ggml-gpt4all-j.bin",
        "messages": [{"role": "user", "content": prompt}],
        "temperature": 0.9
    }
    myheaders = { "Content-Type" : "application/json" }  

    x = requests.post(url, json = myobj, headers=myheaders)
    
    print(x.text)
    
    json_data = json.loads(x.text)

    return json_data["choices"][0]["message"]["content"]


def main():
    # Page setup
    st.set_page_config(page_title="Ask your LLM")
    st.header("Ask your Question 💬")

    # Initialize chat history
    if "messages" not in st.session_state:
        st.session_state.messages = []

    # Display chat messages from history on app rerun
    for message in st.session_state.messages:
        with st.chat_message(message["role"]):
            st.markdown(message["content"])

    # Scroll to bottom
    st.markdown(
        """
        <script>
        var element = document.getElementById("end-of-chat");
        element.scrollIntoView({behavior: "smooth"});
        </script>
        """,
        unsafe_allow_html=True,
    )   

    # React to user input
    if prompt := st.chat_input("What is up?"):
        # Display user message in chat message container
        st.chat_message("user").markdown(prompt)
        # Add user message to chat history
        st.session_state.messages.append({"role": "user", "content": prompt})
        print(f"User has asked the following question: {prompt}")
        
        # Process
        response = ""
        with st.spinner('Processing...'):
            response = ask(prompt)
            
        #response = f"Echo: {prompt}"
        # Display assistant response in chat message container
        with st.chat_message("assistant"):
            st.markdown(response)
        # Add assistant response to chat history
        st.session_state.messages.append({"role": "assistant", "content": response})     

if __name__ == "__main__":
    main()        