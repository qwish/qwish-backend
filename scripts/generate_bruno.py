import os
import re
import json

def clean_filename(name):
    # Remove characters that are invalid in file names
    return re.sub(r'[\\/*?:"<>|]', "", name).strip()

def convert_path_params(path):
    # Convert {param} to :param
    return re.sub(r'\{([^}]+)\}', r':\1', path)

def main():
    base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    api_doc_path = os.path.join(base_dir, "API_DOC.md")
    bruno_dir = os.path.join(base_dir, "bruno")
    
    if not os.path.exists(api_doc_path):
        print(f"Error: API_DOC.md not found at {api_doc_path}")
        return
        
    print(f"Reading API_DOC.md from {api_doc_path}...")
    with open(api_doc_path, "r", encoding="utf-8") as f:
        content = f.read()
        
    # Create bruno directory if not exists
    os.makedirs(bruno_dir, exist_ok=True)
    os.makedirs(os.path.join(bruno_dir, "environments"), exist_ok=True)
    
    # Write bruno.json
    bruno_json = {
        "name": "Qwish API",
        "version": "1",
        "type": "collection",
        "ignore": [
            "node_modules",
            ".git"
        ]
    }
    with open(os.path.join(bruno_dir, "bruno.json"), "w", encoding="utf-8") as f:
        json.dump(bruno_json, f, indent=2)
        
    # Write environment files
    dev_env = (
        "vars {\n"
        "  baseUrl: http://localhost:8080/api/v1\n"
        "  token: \n"
        "}\n"
    )
    with open(os.path.join(bruno_dir, "environments", "development.bru"), "w", encoding="utf-8") as f:
        f.write(dev_env)
        
    prod_env = (
        "vars {\n"
        "  baseUrl: https://api.qwish.in/api/v1\n"
        "  token: \n"
        "}\n"
    )
    with open(os.path.join(bruno_dir, "environments", "production.bru"), "w", encoding="utf-8") as f:
        f.write(prod_env)
        
    # Split content by Level 1 Headers (Sections)
    sections = re.split(r'\n#\s+(\d+\.\s+.*?)\n', content)
    
    # The first element in sections is metadata before the first section
    section_index = 1
    
    while section_index < len(sections):
        section_title = sections[section_index].strip()
        section_content = sections[section_index + 1]
        section_index += 2
        
        # Clean folder name (e.g. "1. Auth" -> "01. Auth")
        match = re.match(r'^(\d+)\.\s*(.*)', section_title)
        if match:
            num = int(match.group(1))
            name = match.group(2).strip()
            folder_name = f"{num:02d}. {name}"
        else:
            folder_name = clean_filename(section_title)
            
        dest_folder = os.path.join(bruno_dir, folder_name)
        
        # Parse endpoints in section content
        # An endpoint starts with "## METHOD `/path`"
        endpoints = re.split(r'\n##\s+(GET|POST|PATCH|DELETE|PUT)\s+', section_content)
        
        if len(endpoints) < 2:
            continue
            
        # Create folder
        os.makedirs(dest_folder, exist_ok=True)
        
        endpoint_index = 1
        seq = 1
        
        while endpoint_index < len(endpoints):
            method = endpoints[endpoint_index].strip()
            endpoint_rest = endpoints[endpoint_index + 1]
            endpoint_index += 2
            
            # Extract route from the rest of the endpoint line
            # It could be: `\`path\`` or just `path` followed by text
            line_match = re.match(r'^`?([^`\n\s]+)`?(.*)', endpoint_rest)
            if not line_match:
                continue
                
            path = line_match.group(1).strip()
            
            # The remaining content for this endpoint
            end_idx = endpoint_rest.find("\n## ")
            if end_idx == -1:
                end_idx = len(endpoint_rest)
            endpoint_body_content = endpoint_rest[:end_idx]
            
            # Parse Auth required
            auth_required = False
            if re.search(r'\*\*Auth required:\*\*\s*Yes', endpoint_body_content, re.IGNORECASE):
                auth_required = True
            elif re.search(r'\*\*Roles:\*\*', endpoint_body_content, re.IGNORECASE):
                # Admin routes often specify roles instead of explicit Auth Required
                auth_required = True
                
            # Parse Request Body (JSON code block)
            req_body = ""
            body_match = re.search(r'### Request Body\s*\n\s*```json\s*\n(.*?)\n\s*```', endpoint_body_content, re.DOTALL)
            if body_match:
                req_body = body_match.group(1).strip()
                
            # Form clean name for Bruno file
            # e.g. /auth/send-otp -> POST send-otp
            clean_path = path.replace("/api/v1", "")
            # Remove leading slash if any
            if clean_path.startswith("/"):
                clean_path = clean_path[1:]
                
            # Convert paths like users/{userId}/profile to clean filename
            file_title = f"{method} {clean_path}"
            file_title = file_title.replace("/", " ")
            file_title = clean_filename(file_title)
            
            # Generate .bru file contents
            meta_name = f"{method} /{clean_path}"
            
            bru_lines = []
            bru_lines.append("meta {")
            bru_lines.append(f"  name: {meta_name}")
            bru_lines.append("  type: http")
            bru_lines.append(f"  seq: {seq}")
            bru_lines.append("}\n")
            
            method_lower = method.lower()
            body_type = "json" if req_body else "none"
            
            bru_lines.append(f"{method_lower} {{")
            # Convert path parameters for Bruno
            bruno_path = convert_path_params(path)
            
            # Make URL local by default, using {{baseUrl}}
            if bruno_path.startswith("/"):
                url = f"{{{{baseUrl}}}}{bruno_path}"
            else:
                url = f"{{{{baseUrl}}}}/{bruno_path}"
                
            bru_lines.append(f"  url: {url}")
            bru_lines.append(f"  body: {body_type}")
            bru_lines.append("  auth: none")
            bru_lines.append("}\n")
            
            if auth_required:
                bru_lines.append("headers {")
                bru_lines.append("  Authorization: Bearer {{token}}")
                bru_lines.append("}\n")
                
            if req_body:
                bru_lines.append("body:json {")
                # Split body into lines and indent them
                for line in req_body.splitlines():
                    bru_lines.append(f"  {line}")
                bru_lines.append("}\n")
                
            file_path = os.path.join(dest_folder, f"{file_title}.bru")
            with open(file_path, "w", encoding="utf-8") as bru_f:
                bru_f.write("\n".join(bru_lines))
                
            print(f"Generated: {folder_name}/{file_title}.bru")
            seq += 1
            
    print("Bruno collection generation complete!")

if __name__ == "__main__":
    main()
